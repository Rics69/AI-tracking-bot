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

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
)

const expenseColumns = `id, user_id, amount, category, spent_at, COALESCE(comment, ''), created_at, updated_at`

// pgForeignKeyViolation is the SQLSTATE of a foreign key violation.
const pgForeignKeyViolation = "23503"

// ExpenseRepository implements expense.Repository.
type ExpenseRepository struct {
	pool *pgxpool.Pool
}

var _ expense.Repository = (*ExpenseRepository)(nil)

func NewExpenseRepository(pool *pgxpool.Pool) *ExpenseRepository {
	return &ExpenseRepository{pool: pool}
}

func (r *ExpenseRepository) Create(ctx context.Context, e expense.Expense) (expense.Expense, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		RETURNING `+expenseColumns,
		e.UserID, e.Amount, e.Category, e.SpentAt, e.Comment)
	created, err := scanExpense(row)
	if err != nil {
		return expense.Expense{}, mapWriteError("create expense", err)
	}
	return created, nil
}

func (r *ExpenseRepository) Get(ctx context.Context, s expense.Scope, id int64) (expense.Expense, error) {
	w := scopeWhere(s)
	w.add("id = ?", id)
	e, err := scanExpense(r.pool.QueryRow(ctx, `SELECT `+expenseColumns+` FROM expenses`+w.sql(), w.args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return expense.Expense{}, expense.ErrNotFound
	}
	if err != nil {
		return expense.Expense{}, fmt.Errorf("get expense: %w", err)
	}
	return e, nil
}

func (r *ExpenseRepository) Update(ctx context.Context, s expense.Scope, e expense.Expense) (expense.Expense, error) {
	w := scopeWhere(s)
	w.add("id = ?", e.ID)
	w.args = append(w.args, e.UserID, e.Amount, e.Category, e.SpentAt, e.Comment)
	n := len(w.args)
	query := fmt.Sprintf(`
		UPDATE expenses
		SET user_id = $%d, amount = $%d, category = $%d, spent_at = $%d,
		    comment = NULLIF($%d, ''), updated_at = now()`+w.sql()+`
		RETURNING `+expenseColumns,
		n-4, n-3, n-2, n-1, n)
	updated, err := scanExpense(r.pool.QueryRow(ctx, query, w.args...))
	if err != nil {
		return expense.Expense{}, mapWriteError("update expense", err)
	}
	return updated, nil
}

func (r *ExpenseRepository) Delete(ctx context.Context, s expense.Scope, id int64) error {
	w := scopeWhere(s)
	w.add("id = ?", id)
	tag, err := r.pool.Exec(ctx, `DELETE FROM expenses`+w.sql(), w.args...)
	if err != nil {
		return fmt.Errorf("delete expense: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return expense.ErrNotFound
	}
	return nil
}

func (r *ExpenseRepository) List(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Page, error) {
	w := filterWhere(s, f)

	page := expense.Page{Items: []expense.Expense{}}
	err := r.pool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(amount), 0) FROM expenses`+w.sql(), w.args...).
		Scan(&page.Total, &page.Sum)
	if err != nil {
		return expense.Page{}, fmt.Errorf("count expenses: %w", err)
	}
	if page.Total == 0 {
		return page, nil
	}

	order := "spent_at DESC, id DESC"
	switch f.Sort {
	case expense.SortByCreatedAt:
		order = "created_at DESC, id DESC"
	case expense.SortByAmount:
		order = "amount DESC, id DESC"
	}
	args := append(w.args, f.Limit, f.Offset)
	query := fmt.Sprintf(`SELECT %s FROM expenses%s ORDER BY %s LIMIT $%d OFFSET $%d`,
		expenseColumns, w.sql(), order, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return expense.Page{}, fmt.Errorf("list expenses: %w", err)
	}
	page.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (expense.Expense, error) {
		return scanExpense(row)
	})
	if err != nil {
		return expense.Page{}, fmt.Errorf("scan expenses: %w", err)
	}
	return page, nil
}

func (r *ExpenseRepository) Summary(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Summary, error) {
	w := filterWhere(s, f)
	rows, err := r.pool.Query(ctx, `
		SELECT category, sum(amount), count(*)
		FROM expenses`+w.sql()+`
		GROUP BY category
		ORDER BY sum(amount) DESC, category`, w.args...)
	if err != nil {
		return expense.Summary{}, fmt.Errorf("summarize expenses: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (expense.CategoryTotal, error) {
		var t expense.CategoryTotal
		err := row.Scan(&t.Category, &t.Sum, &t.Count)
		return t, err
	})
	if err != nil {
		return expense.Summary{}, fmt.Errorf("scan summary: %w", err)
	}
	sum := expense.Summary{Items: items, Sum: decimal.Zero}
	for _, t := range items {
		sum.Sum = sum.Sum.Add(t.Sum)
		sum.Count += t.Count
	}
	return sum, nil
}

func scanExpense(row pgx.Row) (expense.Expense, error) {
	var e expense.Expense
	err := row.Scan(&e.ID, &e.UserID, &e.Amount, &e.Category, &e.SpentAt, &e.Comment, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

func mapWriteError(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return expense.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
		return expense.ErrUnknownUser
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
func scopeWhere(s expense.Scope) *where {
	w := &where{}
	if !s.All() {
		w.add("user_id = ?", s.UserID())
	}
	return w
}

func filterWhere(s expense.Scope, f expense.Filter) *where {
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
