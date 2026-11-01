package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/flow"
)

// FlowRepository implements flow.Store.
type FlowRepository struct {
	pool *pgxpool.Pool
}

var _ flow.Store = (*FlowRepository)(nil)

func NewFlowRepository(pool *pgxpool.Pool) *FlowRepository {
	return &FlowRepository{pool: pool}
}

func (r *FlowRepository) Get(ctx context.Context, userID int64) (flow.State, error) {
	s := flow.State{UserID: userID}
	var data []byte
	err := r.pool.QueryRow(ctx, `SELECT flow, step, data, updated_at FROM user_flows WHERE user_id = $1`, userID).
		Scan(&s.Flow, &s.Step, &data, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return flow.State{}, flow.ErrNone
	}
	if err != nil {
		return flow.State{}, fmt.Errorf("get flow: %w", err)
	}
	if err := json.Unmarshal(data, &s.Data); err != nil {
		return flow.State{}, fmt.Errorf("decode flow: %w", err)
	}
	return s, nil
}

func (r *FlowRepository) Save(ctx context.Context, s flow.State) error {
	data, err := json.Marshal(s.Data)
	if err != nil {
		return fmt.Errorf("encode flow: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO user_flows (user_id, flow, step, data) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			flow = excluded.flow, step = excluded.step, data = excluded.data, updated_at = now()`,
		s.UserID, s.Flow, s.Step, data)
	if err != nil {
		return fmt.Errorf("save flow: %w", err)
	}
	return nil
}

func (r *FlowRepository) Delete(ctx context.Context, userID int64) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM user_flows WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete flow: %w", err)
	}
	return nil
}

// DeleteOlderThan removes abandoned dialogs: their answers are personal data.
func (r *FlowRepository) DeleteOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM user_flows WHERE updated_at < $1`, time.Now().Add(-age))
	if err != nil {
		return 0, fmt.Errorf("delete old flows: %w", err)
	}
	return tag.RowsAffected(), nil
}
