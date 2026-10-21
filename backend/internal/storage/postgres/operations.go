package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

const expenseColumns = `id, user_id, amount, category, spent_at, COALESCE(comment, ''), created_at, updated_at`

// pgForeignKeyViolation is the SQLSTATE of a foreign key violation.
const pgForeignKeyViolation = "23503"

// OperationRepository implements ledger.Repository.
type OperationRepository struct {
	pool *pgxpool.Pool
}

var _ ledger.Repository = (*OperationRepository)(nil)

func NewOperationRepository(pool *pgxpool.Pool) *OperationRepository {
	return &OperationRepository{pool: pool}
}

func (r *OperationRepository) Create(ctx context.Context, e ledger.Operation) (ledger.Operation, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		RETURNING `+expenseColumns,
		e.UserID, e.Amount, e.Category, e.Date, e.Comment)
	created, err := scanExpense(row)
	if err != nil {
		return ledger.Operation{}, mapWriteError("create expense", err)
	}
	return created, nil
}

func (r *OperationRepository) Get(ctx context.Context, s ledger.Scope, id int64) (ledger.Operation, error) {
	w := scopeWhere(s)
	w.add("id = ?", id)
	e, err := scanExpense(r.pool.QueryRow(ctx, `SELECT `+expenseColumns+` FROM expenses`+w.sql(), w.args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.Operation{}, ledger.ErrNotFound
	}
	if err != nil {
		return ledger.Operation{}, fmt.Errorf("get expense: %w", err)
	}
	return e, nil
}

func (r *OperationRepository) Update(ctx context.Context, s ledger.Scope, e ledger.Operation) (ledger.Operation, error) {
	w := scopeWhere(s)
	w.add("id = ?", e.ID)
	if s.FamilyID() != 0 {
		// The new owner must be in the family too: the head cannot hand an
		// expense to a stranger. $1 is the family ID added by scopeWhere.
		w.add("EXISTS (SELECT 1 FROM users WHERE family_id = $1 AND telegram_id = ?)", e.UserID)
	}
	w.args = append(w.args, e.UserID, e.Amount, e.Category, e.Date, e.Comment)
	n := len(w.args)
	query := fmt.Sprintf(`
		UPDATE expenses
		SET user_id = $%d, amount = $%d, category = $%d, spent_at = $%d,
		    comment = NULLIF($%d, ''), updated_at = now()`+w.sql()+`
		RETURNING `+expenseColumns,
		n-4, n-3, n-2, n-1, n)
	updated, err := scanExpense(r.pool.QueryRow(ctx, query, w.args...))
	if err != nil {
		return ledger.Operation{}, mapWriteError("update expense", err)
	}
	return updated, nil
}

func (r *OperationRepository) Delete(ctx context.Context, s ledger.Scope, id int64) error {
	w := scopeWhere(s)
	w.add("id = ?", id)
	tag, err := r.pool.Exec(ctx, `DELETE FROM expenses`+w.sql(), w.args...)
	if err != nil {
		return fmt.Errorf("delete expense: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ledger.ErrNotFound
	}
	return nil
}

func (r *OperationRepository) List(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error) {
	w := filterWhere(s, f)

	page := ledger.Page{Items: []ledger.Operation{}}
	err := r.pool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(amount), 0) FROM expenses`+w.sql(), w.args...).
		Scan(&page.Total, &page.Sum)
	if err != nil {
		return ledger.Page{}, fmt.Errorf("count expenses: %w", err)
	}
	if page.Total == 0 {
		return page, nil
	}

	order := "spent_at DESC, id DESC"
	switch f.Sort {
	case ledger.SortByCreatedAt:
		order = "created_at DESC, id DESC"
	case ledger.SortByAmount:
		order = "amount DESC, id DESC"
	}
	args := append(w.args, f.Limit, f.Offset)
	query := fmt.Sprintf(`SELECT %s FROM expenses%s ORDER BY %s LIMIT $%d OFFSET $%d`,
		expenseColumns, w.sql(), order, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return ledger.Page{}, fmt.Errorf("list expenses: %w", err)
	}
	page.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (ledger.Operation, error) {
		return scanExpense(row)
	})
	if err != nil {
		return ledger.Page{}, fmt.Errorf("scan expenses: %w", err)
	}
	return page, nil
}

func (r *OperationRepository) Summary(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Summary, error) {
	w := filterWhere(s, f)
	rows, err := r.pool.Query(ctx, `
		SELECT category, sum(amount), count(*)
		FROM expenses`+w.sql()+`
		GROUP BY category
		ORDER BY sum(amount) DESC, category`, w.args...)
	if err != nil {
		return ledger.Summary{}, fmt.Errorf("summarize expenses: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ledger.CategoryTotal, error) {
		var t ledger.CategoryTotal
		err := row.Scan(&t.Category, &t.Sum, &t.Count)
		return t, err
	})
	if err != nil {
		return ledger.Summary{}, fmt.Errorf("scan summary: %w", err)
	}
	sum := ledger.Summary{Items: items, Sum: decimal.Zero}
	for _, t := range items {
		sum.Sum = sum.Sum.Add(t.Sum)
		sum.Count += t.Count
	}
	return sum, nil
}

func (r *OperationRepository) Aggregate(ctx context.Context, s ledger.Scope, f ledger.Filter, b ledger.Bucket) ([]ledger.BucketTotal, error) {
	w := filterWhere(s, f)
	// The bucket is passed as data, never concatenated into SQL.
	args := append(w.args, string(b))
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT date_trunc($%d, spent_at)::date AS start, category, sum(amount), count(*)
		FROM expenses%s
		GROUP BY 1, 2`, len(args), w.sql()), args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate expenses: %w", err)
	}
	totals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ledger.BucketTotal, error) {
		var t ledger.BucketTotal
		err := row.Scan(&t.Start, &t.Category, &t.Sum, &t.Count)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan aggregates: %w", err)
	}
	return totals, nil
}

func scanExpense(row pgx.Row) (ledger.Operation, error) {
	var e ledger.Operation
	err := row.Scan(&e.ID, &e.UserID, &e.Amount, &e.Category, &e.Date, &e.Comment, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

func mapWriteError(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
		return ledger.ErrUnknownUser
	}
	return fmt.Errorf("%s: %w", op, err)
}

// where accumulates AND-ed conditions with positional arguments.
// Conditions use "?" as the placeholder for their single argument.
type where struct {
	conds []string
	args  []any
}

func (w *where) add(cond string, arg any) {
	w.args = append(w.args, arg)
	w.conds = append(w.conds, strings.Replace(cond, "?", "$"+strconv.Itoa(len(w.args)), 1))
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

// scopeWhere restricts a query to the scope. The zero scope has user ID 0,
// which no Telegram user has, so it matches nothing.
func scopeWhere(s ledger.Scope) *where {
	w := &where{}
	if s.FamilyID() != 0 {
		w.add("user_id IN (SELECT telegram_id FROM users WHERE family_id = ?)", s.FamilyID())
	} else {
		w.add("user_id = ?", s.UserID())
	}
	return w
}

func filterWhere(s ledger.Scope, f ledger.Filter) *where {
	w := scopeWhere(s)
	if f.UserID != nil {
		w.add("user_id = ?", *f.UserID)
	}
	if f.Category != nil {
		w.add("category = ?", *f.Category)
	}
	if f.From != nil {
		w.add("spent_at >= ?", *f.From)
	}
	if f.To != nil {
		w.add("spent_at <= ?", *f.To)
	}
	if f.Query != "" {
		w.add("comment ILIKE ?", "%"+escapeLike(f.Query)+"%")
	}
	return w
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(s string) string { return likeEscaper.Replace(s) }
