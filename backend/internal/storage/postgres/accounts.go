package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
)

// AccountRepository implements accounts.Store.
type AccountRepository struct {
	pool *pgxpool.Pool
}

var _ accounts.Store = (*AccountRepository)(nil)

func NewAccountRepository(pool *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{pool: pool}
}

const accountColumns = `id, user_id, name, kind, currency, opening_balance, is_default, archived_at, created_at`

func (r *AccountRepository) EnsureDefault(ctx context.Context, userID int64, name string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO accounts (user_id, name, is_default) VALUES ($1, $2, true)
		ON CONFLICT (user_id) WHERE is_default DO NOTHING`, userID, name)
	if err != nil {
		return fmt.Errorf("ensure default account: %w", err)
	}
	return nil
}

func (r *AccountRepository) List(ctx context.Context, userID int64) ([]accounts.Account, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+accountColumns+` FROM accounts WHERE user_id = $1
		ORDER BY is_default DESC, created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[accounts.Account])
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return out, nil
}

func (r *AccountRepository) one(row pgx.Row, what string) (accounts.Account, error) {
	var a accounts.Account
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.Kind, &a.Currency, &a.Opening, &a.IsDefault, &a.ArchivedAt, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Account{}, accounts.ErrNotFound
	}
	if err != nil {
		return accounts.Account{}, fmt.Errorf("%s: %w", what, err)
	}
	return a, nil
}

func (r *AccountRepository) Get(ctx context.Context, userID, id int64) (accounts.Account, error) {
	return r.one(r.pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts WHERE user_id = $1 AND id = $2`, userID, id), "get account")
}

func (r *AccountRepository) Create(ctx context.Context, a accounts.Account) (accounts.Account, error) {
	return r.one(r.pool.QueryRow(ctx, `
		INSERT INTO accounts (user_id, name, kind, currency, opening_balance) VALUES ($1, $2, $3, $4, $5)
		RETURNING `+accountColumns, a.UserID, a.Name, a.Kind, a.Currency, a.Opening), "create account")
}

func (r *AccountRepository) Update(ctx context.Context, a accounts.Account) (accounts.Account, error) {
	return r.one(r.pool.QueryRow(ctx, `
		UPDATE accounts SET name = $3, kind = $4, opening_balance = $5, archived_at = $6
		WHERE user_id = $1 AND id = $2
		RETURNING `+accountColumns, a.UserID, a.ID, a.Name, a.Kind, a.Opening, a.ArchivedAt), "update account")
}

func (r *AccountRepository) Operated(ctx context.Context, userID int64) (map[int64]decimal.Decimal, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, COALESCE(sum(
		    CASE WHEN o.kind = 'income' THEN 1 ELSE -1 END *
		    CASE WHEN a.currency = 'RUB' THEN o.amount ELSE COALESCE(o.original_amount, o.amount) END), 0)
		FROM accounts a
		JOIN operations o ON o.user_id = a.user_id AND (o.account_id = a.id OR (o.account_id IS NULL AND a.is_default))
		WHERE a.user_id = $1
		GROUP BY a.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("sum operations per account: %w", err)
	}
	defer rows.Close()
	out := map[int64]decimal.Decimal{}
	for rows.Next() {
		var (
			id  int64
			sum decimal.Decimal
		)
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, fmt.Errorf("sum operations per account: %w", err)
		}
		out[id] = sum
	}
	return out, rows.Err()
}

const transferColumns = `id, user_id, from_id, to_id, amount_from, amount_to, occurred_on, COALESCE(comment, ''), created_at`

func (r *AccountRepository) AddTransfer(ctx context.Context, t accounts.Transfer) (accounts.Transfer, error) {
	var out accounts.Transfer
	err := r.pool.QueryRow(ctx, `
		INSERT INTO transfers (user_id, from_id, to_id, amount_from, amount_to, occurred_on, comment)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		RETURNING `+transferColumns, t.UserID, t.FromID, t.ToID, t.AmountFrom, t.AmountTo, t.Date, t.Comment).
		Scan(&out.ID, &out.UserID, &out.FromID, &out.ToID, &out.AmountFrom, &out.AmountTo, &out.Date, &out.Comment, &out.CreatedAt)
	if err != nil {
		return accounts.Transfer{}, fmt.Errorf("add transfer: %w", err)
	}
	return out, nil
}

func (r *AccountRepository) Transfers(ctx context.Context, userID int64) ([]accounts.Transfer, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+transferColumns+` FROM transfers WHERE user_id = $1
		ORDER BY occurred_on DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list transfers: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[accounts.Transfer])
	if err != nil {
		return nil, fmt.Errorf("list transfers: %w", err)
	}
	return out, nil
}

func (r *AccountRepository) DeleteTransfer(ctx context.Context, userID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM transfers WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("delete transfer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return accounts.ErrNotFound
	}
	return nil
}
