package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReportRunRepository remembers which weekly reports were handled.
type ReportRunRepository struct {
	pool *pgxpool.Pool
}

func NewReportRunRepository(pool *pgxpool.Pool) *ReportRunRepository {
	return &ReportRunRepository{pool: pool}
}

// Claim marks the report of a week as taken by this process. It returns false
// if it was already claimed: by an earlier run, or by another bot instance.
func (r *ReportRunRepository) Claim(ctx context.Context, weekStart time.Time, userID int64) (bool, error) {
	var claimed int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO report_runs (week_start, user_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING RETURNING user_id`, weekStart, userID).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim report: %w", err)
	}
	return true, nil
}

// Release undoes a claim when sending failed, so the next tick retries.
func (r *ReportRunRepository) Release(ctx context.Context, weekStart time.Time, userID int64) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM report_runs WHERE week_start = $1 AND user_id = $2`, weekStart, userID); err != nil {
		return fmt.Errorf("release report: %w", err)
	}
	return nil
}
