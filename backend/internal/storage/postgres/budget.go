package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// BudgetRepository implements budget.Repository.
type BudgetRepository struct {
	pool *pgxpool.Pool
}

var _ budget.Repository = (*BudgetRepository)(nil)

func NewBudgetRepository(pool *pgxpool.Pool) *BudgetRepository {
	return &BudgetRepository{pool: pool}
}

// Plan.

const planColumns = `user_id, paydays, expected_income, goal_contribution, reminder_hour, reminders, updated_at`

func (r *BudgetRepository) Plan(ctx context.Context, userID int64) (budget.Plan, error) {
	p, err := scanPlan(r.pool.QueryRow(ctx, `SELECT `+planColumns+` FROM budget_plans WHERE user_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return budget.Plan{}, budget.ErrNotFound
	}
	if err != nil {
		return budget.Plan{}, fmt.Errorf("get plan: %w", err)
	}
	return p, nil
}

func (r *BudgetRepository) SavePlan(ctx context.Context, p budget.Plan) (budget.Plan, error) {
	days := make([]int32, len(p.Paydays))
	for i, d := range p.Paydays {
		days[i] = int32(d)
	}
	saved, err := scanPlan(r.pool.QueryRow(ctx, `
		INSERT INTO budget_plans (user_id, paydays, expected_income, goal_contribution, reminder_hour, reminders)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			paydays = excluded.paydays, expected_income = excluded.expected_income,
			goal_contribution = excluded.goal_contribution, reminder_hour = excluded.reminder_hour,
			reminders = excluded.reminders, updated_at = now()
		RETURNING `+planColumns,
		p.UserID, days, p.ExpectedIncome, p.GoalContribution, p.ReminderHour, p.Reminders))
	if err != nil {
		return budget.Plan{}, mapBudgetError("save plan", err)
	}
	return saved, nil
}

func (r *BudgetRepository) DeletePlan(ctx context.Context, userID int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM budget_plans WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return budget.ErrNotFound
	}
	return nil
}

func scanPlan(row pgx.Row) (budget.Plan, error) {
	var (
		p    budget.Plan
		days []int32
	)
	err := row.Scan(&p.UserID, &days, &p.ExpectedIncome, &p.GoalContribution, &p.ReminderHour, &p.Reminders, &p.UpdatedAt)
	p.Paydays = make([]int, len(days))
	for i, d := range days {
		p.Paydays[i] = int(d)
	}
	return p, err
}

// Goals.

// goalSelect reads goals with their savings; the caller adds WHERE.
const goalSelect = `
	SELECT g.id, g.user_id, g.title, g.target,
	       COALESCE((SELECT sum(amount) FROM goal_deposits d WHERE d.goal_id = g.id), 0),
	       g.deadline, g.achieved_on, g.created_at, g.updated_at
	FROM goals g`

func (r *BudgetRepository) Goals(ctx context.Context, userID int64) ([]budget.Goal, error) {
	rows, err := r.pool.Query(ctx, goalSelect+` WHERE g.user_id = $1 ORDER BY g.created_at, g.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	goals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (budget.Goal, error) { return scanGoal(row) })
	if err != nil {
		return nil, fmt.Errorf("scan goals: %w", err)
	}
	return goals, nil
}

func (r *BudgetRepository) Goal(ctx context.Context, userID, id int64) (budget.Goal, error) {
	return r.goal(ctx, r.pool, userID, id)
}

func (r *BudgetRepository) goal(ctx context.Context, q querier, userID, id int64) (budget.Goal, error) {
	g, err := scanGoal(q.QueryRow(ctx, goalSelect+` WHERE g.user_id = $1 AND g.id = $2`, userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return budget.Goal{}, budget.ErrNotFound
	}
	if err != nil {
		return budget.Goal{}, fmt.Errorf("get goal: %w", err)
	}
	return g, nil
}

func (r *BudgetRepository) CreateGoal(ctx context.Context, g budget.Goal) (budget.Goal, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO goals (user_id, title, target, deadline) VALUES ($1, $2, $3, $4) RETURNING id`,
		g.UserID, g.Title, g.Target, g.Deadline).Scan(&id)
	if err != nil {
		return budget.Goal{}, mapBudgetError("create goal", err)
	}
	return r.Goal(ctx, g.UserID, id)
}

func (r *BudgetRepository) UpdateGoal(ctx context.Context, g budget.Goal) (budget.Goal, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE goals SET title = $3, target = $4, deadline = $5, achieved_on = $6, updated_at = now()
		WHERE user_id = $1 AND id = $2`,
		g.UserID, g.ID, g.Title, g.Target, g.Deadline, g.AchievedOn)
	if err != nil {
		return budget.Goal{}, fmt.Errorf("update goal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return budget.Goal{}, budget.ErrNotFound
	}
	return r.Goal(ctx, g.UserID, g.ID)
}

func (r *BudgetRepository) DeleteGoal(ctx context.Context, userID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM goals WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("delete goal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return budget.ErrNotFound
	}
	return nil
}

func (r *BudgetRepository) AddDeposit(ctx context.Context, d budget.Deposit, achievedOn *time.Time) (budget.Goal, error) {
	var g budget.Goal
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE goals SET achieved_on = $3, updated_at = now() WHERE user_id = $1 AND id = $2`,
			d.UserID, d.GoalID, achievedOn)
		if err != nil {
			return fmt.Errorf("update goal: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return budget.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO goal_deposits (goal_id, user_id, amount, occurred_on) VALUES ($1, $2, $3, $4)`,
			d.GoalID, d.UserID, d.Amount, d.Date); err != nil {
			return fmt.Errorf("insert deposit: %w", err)
		}
		g, err = r.goal(ctx, tx, d.UserID, d.GoalID)
		return err
	})
	if err != nil {
		return budget.Goal{}, err
	}
	return g, nil
}

func (r *BudgetRepository) Saved(ctx context.Context, userID int64, from, to time.Time) (decimal.Decimal, error) {
	var sum decimal.Decimal
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(amount), 0) FROM goal_deposits
		WHERE user_id = $1 AND occurred_on BETWEEN $2 AND $3`, userID, from, to).Scan(&sum)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sum deposits: %w", err)
	}
	return sum, nil
}

func scanGoal(row pgx.Row) (budget.Goal, error) {
	var g budget.Goal
	err := row.Scan(&g.ID, &g.UserID, &g.Title, &g.Target, &g.Saved, &g.Deadline, &g.AchievedOn, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

// Regular payments.

const paymentSelect = `
	SELECT p.id, p.user_id, p.title, p.amount, p.day, p.category,
	       (SELECT max(occurred_on) FROM operations o WHERE o.regular_payment_id = p.id),
	       p.created_at
	FROM regular_payments p`

func (r *BudgetRepository) Payments(ctx context.Context, userID int64) ([]budget.RegularPayment, error) {
	rows, err := r.pool.Query(ctx, paymentSelect+` WHERE p.user_id = $1 ORDER BY p.day, p.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list regular payments: %w", err)
	}
	payments, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (budget.RegularPayment, error) {
		return scanPayment(row)
	})
	if err != nil {
		return nil, fmt.Errorf("scan regular payments: %w", err)
	}
	return payments, nil
}

func (r *BudgetRepository) Payment(ctx context.Context, userID, id int64) (budget.RegularPayment, error) {
	p, err := scanPayment(r.pool.QueryRow(ctx, paymentSelect+` WHERE p.user_id = $1 AND p.id = $2`, userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return budget.RegularPayment{}, budget.ErrNotFound
	}
	if err != nil {
		return budget.RegularPayment{}, fmt.Errorf("get regular payment: %w", err)
	}
	return p, nil
}

func (r *BudgetRepository) CreatePayment(ctx context.Context, p budget.RegularPayment) (budget.RegularPayment, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO regular_payments (user_id, title, amount, day, category) VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, p.UserID, p.Title, p.Amount, p.Day, p.Category).Scan(&id)
	if err != nil {
		return budget.RegularPayment{}, mapBudgetError("create regular payment", err)
	}
	return r.Payment(ctx, p.UserID, id)
}

func (r *BudgetRepository) UpdatePayment(ctx context.Context, p budget.RegularPayment) (budget.RegularPayment, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE regular_payments SET title = $3, amount = $4, day = $5, category = $6
		WHERE user_id = $1 AND id = $2`, p.UserID, p.ID, p.Title, p.Amount, p.Day, p.Category)
	if err != nil {
		return budget.RegularPayment{}, fmt.Errorf("update regular payment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return budget.RegularPayment{}, budget.ErrNotFound
	}
	return r.Payment(ctx, p.UserID, p.ID)
}

func (r *BudgetRepository) DeletePayment(ctx context.Context, userID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM regular_payments WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return fmt.Errorf("delete regular payment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return budget.ErrNotFound
	}
	return nil
}

func scanPayment(row pgx.Row) (budget.RegularPayment, error) {
	var p budget.RegularPayment
	err := row.Scan(&p.ID, &p.UserID, &p.Title, &p.Amount, &p.Day, &p.Category, &p.LastPaidOn, &p.CreatedAt)
	return p, err
}

func (r *BudgetRepository) Reminded(ctx context.Context) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id FROM budget_plans WHERE reminders
		UNION
		SELECT DISTINCT user_id FROM regular_payments`)
	if err != nil {
		return nil, fmt.Errorf("list reminded users: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("scan reminded users: %w", err)
	}
	return ids, nil
}

// querier is a pool or a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// mapBudgetError turns a missing owner into ledger.ErrUnknownUser, like operations.
func mapBudgetError(op string, err error) error {
	if mapped := mapWriteError(op, err); errors.Is(mapped, ledger.ErrUnknownUser) {
		return mapped
	}
	return fmt.Errorf("%s: %w", op, err)
}
