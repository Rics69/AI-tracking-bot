package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
