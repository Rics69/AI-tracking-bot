package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationRunRepository remembers which messages the bot started itself
// were handled: a weekly report per week, a reminder per day.
type NotificationRunRepository struct {
	pool *pgxpool.Pool
}

func NewNotificationRunRepository(pool *pgxpool.Pool) *NotificationRunRepository {
	return &NotificationRunRepository{pool: pool}
}

// Claim marks a notification (kind, period) of a user as taken by this
// process. It returns false if it was already claimed: by an earlier run, or
// by another bot instance.
func (r *NotificationRunRepository) Claim(ctx context.Context, kind, period string, userID int64) (bool, error) {
	var claimed int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO notification_runs (kind, period_key, user_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING RETURNING user_id`, kind, period, userID).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim notification: %w", err)
	}
	return true, nil
}

// Release undoes a claim when sending failed, so the next tick retries.
func (r *NotificationRunRepository) Release(ctx context.Context, kind, period string, userID int64) error {
	if _, err := r.pool.Exec(ctx, `
		DELETE FROM notification_runs WHERE kind = $1 AND period_key = $2 AND user_id = $3`,
		kind, period, userID); err != nil {
		return fmt.Errorf("release notification: %w", err)
	}
	return nil
}
