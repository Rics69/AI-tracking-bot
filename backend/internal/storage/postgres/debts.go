package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
)

// DebtRepository implements debts.Store.
type DebtRepository struct {
	pool *pgxpool.Pool
}

var _ debts.Store = (*DebtRepository)(nil)

func NewDebtRepository(pool *pgxpool.Pool) *DebtRepository {
	return &DebtRepository{pool: pool}
}

const debtSelect = `
	SELECT d.id, d.user_id, d.name, d.kind, d.principal, d.rate_apr, d.min_payment, d.due_day::int, d.started_on, d.closed_at, d.balance
	FROM (
		SELECT x.*, x.principal - COALESCE((SELECT sum(p.amount) FROM debt_payments p WHERE p.debt_id = x.id), 0) AS balance
		FROM debts x
	) d`

func (r *DebtRepository) list(ctx context.Context, where string, args ...any) ([]debts.Debt, error) {
	rows, err := r.pool.Query(ctx, debtSelect+` WHERE `+where+` ORDER BY d.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list debts: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[debts.Debt])
	if err != nil {
		return nil, fmt.Errorf("list debts: %w", err)
	}
	return out, nil
}

func (r *DebtRepository) List(ctx context.Context, userID int64) ([]debts.Debt, error) {
	return r.list(ctx, `d.user_id = $1`, userID)
}

func (r *DebtRepository) Open(ctx context.Context) ([]debts.Debt, error) {
	return r.list(ctx, `d.balance > 0`)
}

func (r *DebtRepository) Get(ctx context.Context, userID, id int64) (debts.Debt, error) {
	list, err := r.list(ctx, `d.user_id = $1 AND d.id = $2`, userID, id)
	if err != nil {
		return debts.Debt{}, err
	}
	if len(list) == 0 {
		return debts.Debt{}, debts.ErrNotFound
	}
	return list[0], nil
}

func (r *DebtRepository) Create(ctx context.Context, d debts.Debt) (debts.Debt, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO debts (user_id, name, kind, principal, rate_apr, min_payment, due_day, started_on)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		d.UserID, d.Name, d.Kind, d.Principal, d.Rate, d.MinPayment, d.DueDay, d.StartedOn).Scan(&id)
	if err != nil {
		return debts.Debt{}, fmt.Errorf("create debt: %w", err)
	}
	return r.Get(ctx, d.UserID, id)
}

func (r *DebtRepository) Update(ctx context.Context, d debts.Debt) (debts.Debt, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE debts SET name = $3, kind = $4, principal = $5, rate_apr = $6, min_payment = $7, due_day = $8, closed_at = $9
		WHERE user_id = $1 AND id = $2`,
		d.UserID, d.ID, d.Name, d.Kind, d.Principal, d.Rate, d.MinPayment, d.DueDay, d.ClosedAt)
	if err != nil {
		return debts.Debt{}, fmt.Errorf("update debt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return debts.Debt{}, debts.ErrNotFound
	}
	return r.Get(ctx, d.UserID, d.ID)
}

func (r *DebtRepository) Delete(ctx context.Context, userID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM debts WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("delete debt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return debts.ErrNotFound
	}
	return nil
}

const paymentColumns = `id, debt_id, user_id, amount, paid_on, operation_id`

func (r *DebtRepository) AddPayment(ctx context.Context, p debts.Payment) (debts.Payment, error) {
	rows, err := r.pool.Query(ctx, `
		INSERT INTO debt_payments (debt_id, user_id, amount, paid_on, operation_id) VALUES ($1, $2, $3, $4, $5)
		RETURNING `+paymentColumns, p.DebtID, p.UserID, p.Amount, p.Date, p.OperationID)
	if err != nil {
		return debts.Payment{}, fmt.Errorf("add debt payment: %w", err)
	}
	saved, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[debts.Payment])
	if err != nil {
		return debts.Payment{}, fmt.Errorf("add debt payment: %w", err)
	}
	return saved, nil
}

func (r *DebtRepository) Payments(ctx context.Context, userID, debtID int64) ([]debts.Payment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+paymentColumns+` FROM debt_payments
		WHERE user_id = $1 AND debt_id = $2 ORDER BY paid_on DESC, id DESC`, userID, debtID)
	if err != nil {
		return nil, fmt.Errorf("list debt payments: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[debts.Payment])
	if err != nil {
		return nil, fmt.Errorf("list debt payments: %w", err)
	}
	return out, nil
}

func (r *DebtRepository) DeletePayment(ctx context.Context, userID, id int64) (debts.Payment, error) {
	rows, err := r.pool.Query(ctx, `DELETE FROM debt_payments WHERE user_id = $1 AND id = $2 RETURNING `+paymentColumns, userID, id)
	if err != nil {
		return debts.Payment{}, fmt.Errorf("delete debt payment: %w", err)
	}
	p, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[debts.Payment])
	if errors.Is(err, pgx.ErrNoRows) {
		return debts.Payment{}, debts.ErrNotFound
	}
	if err != nil {
		return debts.Payment{}, fmt.Errorf("delete debt payment: %w", err)
	}
	return p, nil
}

func (r *DebtRepository) ClosedUsers(ctx context.Context) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT user_id FROM debts WHERE closed_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("list closed debts: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("list closed debts: %w", err)
	}
	return out, nil
}

func (r *DebtRepository) SaveSnapshot(ctx context.Context, userID int64, s debts.Snapshot) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO net_worth_snapshots (user_id, month, assets, debts) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, month) DO UPDATE SET assets = excluded.assets, debts = excluded.debts`,
		userID, s.Month, s.Assets, s.Debts)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (r *DebtRepository) Snapshots(ctx context.Context, userID int64, from time.Time) ([]debts.Snapshot, error) {
	rows, err := r.pool.Query(ctx, `SELECT month, assets, debts FROM net_worth_snapshots
		WHERE user_id = $1 AND month >= $2 ORDER BY month`, userID, from)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[debts.Snapshot])
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	return out, nil
}
