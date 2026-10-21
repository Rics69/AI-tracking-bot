package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/usage"
)

// UsageRepository implements usage.Store.
type UsageRepository struct {
	pool *pgxpool.Pool
}

var _ usage.Store = (*UsageRepository)(nil)

func NewUsageRepository(pool *pgxpool.Pool) *UsageRepository {
	return &UsageRepository{pool: pool}
}

func (r *UsageRepository) Add(ctx context.Context, userID int64, day time.Time, u llm.Usage, failed bool) error {
	failures := 0
	if failed {
		failures = 1
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO llm_usage AS u (user_id, day, requests, failures, prompt_tokens, cached_tokens, completion_tokens)
		VALUES ($1, $2, 1, $3, $4, $5, $6)
		ON CONFLICT (user_id, day) DO UPDATE SET
		    requests = u.requests + 1,
		    failures = u.failures + EXCLUDED.failures,
		    prompt_tokens = u.prompt_tokens + EXCLUDED.prompt_tokens,
		    cached_tokens = u.cached_tokens + EXCLUDED.cached_tokens,
		    completion_tokens = u.completion_tokens + EXCLUDED.completion_tokens`,
		userID, day, failures, u.PromptTokens, u.CachedTokens, u.CompletionTokens)
	if err != nil {
		return fmt.Errorf("add llm usage: %w", err)
	}
	return nil
}

func (r *UsageRepository) Get(ctx context.Context, userID int64, day time.Time) (usage.Day, error) {
	var d usage.Day
	err := r.pool.QueryRow(ctx, `
		SELECT requests, failures, prompt_tokens, cached_tokens, completion_tokens
		FROM llm_usage WHERE user_id = $1 AND day = $2`, userID, day).
		Scan(&d.Requests, &d.Failures, &d.PromptTokens, &d.CachedTokens, &d.CompletionTokens)
	if errors.Is(err, pgx.ErrNoRows) {
		return usage.Day{}, nil
	}
	if err != nil {
		return usage.Day{}, fmt.Errorf("get llm usage: %w", err)
	}
	return d, nil
}
