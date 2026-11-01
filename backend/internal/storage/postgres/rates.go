package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
)

// RateRepository implements rates.Store.
type RateRepository struct {
	pool *pgxpool.Pool
}

var _ rates.Store = (*RateRepository)(nil)

func NewRateRepository(pool *pgxpool.Pool) *RateRepository {
	return &RateRepository{pool: pool}
}

func (r *RateRepository) Save(ctx context.Context, items []rates.Rate) error {
	batch := &pgx.Batch{}
	for _, it := range items {
		batch.Queue(`INSERT INTO rates (code, date, value) VALUES ($1, $2, $3)
			ON CONFLICT (code, date) DO UPDATE SET value = excluded.value`, it.Code, it.Date, it.Value)
	}
	if err := r.pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("save rates: %w", err)
	}
	return nil
}

// On is the rate of a date: the last one before it, or the earliest we have
// when the date is older than the table.
func (r *RateRepository) On(ctx context.Context, code string, date time.Time) (decimal.Decimal, error) {
	var value decimal.Decimal
	err := r.pool.QueryRow(ctx, `
		SELECT value FROM rates WHERE code = $1
		ORDER BY (date <= $2) DESC, CASE WHEN date <= $2 THEN $2::date - date ELSE date - $2::date END
		LIMIT 1`, code, date).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, rates.ErrNoRate
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("rate on a date: %w", err)
	}
	return value, nil
}

// Now is the latest rate of a code.
func (r *RateRepository) Now(ctx context.Context, code string) (decimal.Decimal, error) {
	return r.On(ctx, code, time.Now())
}

func (r *RateRepository) Dates(ctx context.Context, code string, from time.Time) ([]time.Time, error) {
	rows, err := r.pool.Query(ctx, `SELECT date FROM rates WHERE code = $1 AND date >= $2`, code, from)
	if err != nil {
		return nil, fmt.Errorf("list rate dates: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		return nil, fmt.Errorf("list rate dates: %w", err)
	}
	return out, nil
}

func (r *RateRepository) Latest(ctx context.Context, codes []string) ([]rates.Rate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (code) code, date, value FROM rates
		WHERE code = ANY($1) ORDER BY code DESC, date DESC`, codes)
	if err != nil {
		return nil, fmt.Errorf("latest rates: %w", err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[rates.Rate])
	if err != nil {
		return nil, fmt.Errorf("latest rates: %w", err)
	}
	return items, nil
}
