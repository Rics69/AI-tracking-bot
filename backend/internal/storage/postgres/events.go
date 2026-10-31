package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
)

// EventRepository implements events.Store.
type EventRepository struct {
	pool *pgxpool.Pool
}

var _ events.Store = (*EventRepository)(nil)

func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

func (r *EventRepository) Add(ctx context.Context, userID int64, name string, props events.Props) error {
	if props == nil {
		props = events.Props{}
	}
	var owner *int64
	if userID != 0 {
		owner = &userID
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO events (user_id, name, props) VALUES ($1, $2, $3)`, owner, name, props); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}
