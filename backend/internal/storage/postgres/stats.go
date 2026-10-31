package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/stats"
)

// StatsRepository implements stats.Source. It reads counts across all users:
// the owner sees how the service is used, never anyone's money.
type StatsRepository struct {
	pool *pgxpool.Pool
}

var _ stats.Source = (*StatsRepository)(nil)

func NewStatsRepository(pool *pgxpool.Pool) *StatsRepository {
	return &StatsRepository{pool: pool}
}

func (r *StatsRepository) Snapshot(ctx context.Context, now, today, monthStart, packageSince time.Time) (stats.Snapshot, error) {
	s := stats.Snapshot{At: now}
	day, week := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour)
	err := r.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE consented_at IS NOT NULL),
		       count(*) FILTER (WHERE blocked_at IS NOT NULL),
		       count(*) FILTER (WHERE created_at >= $1),
		       count(*) FILTER (WHERE created_at >= $2),
		       count(*) FILTER (WHERE last_seen_at >= $1),
		       count(*) FILTER (WHERE last_seen_at >= $2)
		FROM users`, day, week).
		Scan(&s.Users, &s.Consented, &s.Blocked, &s.NewDay, &s.NewWeek, &s.ActiveDay, &s.ActiveWeek)
	if err != nil {
		return stats.Snapshot{}, fmt.Errorf("count users: %w", err)
	}
	err = r.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM operations WHERE created_at >= $1),
		       (SELECT count(DISTINCT user_id) FROM operations WHERE created_at >= $1),
		       (SELECT count(*) FROM budget_plans),
		       (SELECT count(DISTINCT user_id) FROM goals),
		       (SELECT count(DISTINCT user_id) FROM regular_payments),
		       (SELECT count(*) FROM (SELECT 1 FROM users GROUP BY family_id HAVING count(*) > 1) f),
		       (SELECT count(*) FROM users WHERE family_id IN (
		            SELECT family_id FROM users GROUP BY family_id HAVING count(*) > 1))`, week).
		Scan(&s.OperationsWeek, &s.RecordersWeek, &s.WithPlan, &s.WithGoals, &s.WithPayments, &s.Families, &s.InFamilies)
	if err != nil {
		return stats.Snapshot{}, fmt.Errorf("count usage of features: %w", err)
	}
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(prompt_tokens + completion_tokens) FILTER (WHERE day >= $1), 0),
		       COALESCE(sum(prompt_tokens + completion_tokens) FILTER (WHERE day >= $2), 0),
		       count(DISTINCT day) FILTER (WHERE day >= $2)
		FROM llm_usage`, packageSince, today.AddDate(0, 0, -6)).
		Scan(&s.Package.Spent, &s.Package.WeekTokens, &s.Package.WeekDays)
	if err != nil {
		return stats.Snapshot{}, fmt.Errorf("sum package usage: %w", err)
	}
	err = r.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE props->>'via' = $3),
		       count(*) FILTER (WHERE props->>'via' = $4),
		       count(*) FILTER (WHERE props->>'via' = $5)
		FROM events WHERE name IN ($1, $2) AND created_at >= $6`,
		events.ExpenseAdded, events.IncomeAdded, events.ViaFast, events.ViaAI, events.ViaMiniApp, week).
		Scan(&s.Recorded.Fast, &s.Recorded.AI, &s.Recorded.MiniApp)
	if err != nil {
		return stats.Snapshot{}, fmt.Errorf("count recorded operations: %w", err)
	}
	if s.Sources, err = r.sources(ctx, week); err != nil {
		return stats.Snapshot{}, err
	}
	if s.Today, err = r.usage(ctx, today, today); err != nil {
		return stats.Snapshot{}, err
	}
	if s.Month, err = r.usage(ctx, monthStart, today); err != nil {
		return stats.Snapshot{}, err
	}
	return s, nil
}

// sources counts new users by where they came from. Only the biggest are
// listed: the list ends up in one Telegram message.
func (r *StatsRepository) sources(ctx context.Context, since time.Time) ([]stats.SourceCount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(first_source, 'direct'), count(*)
		FROM users WHERE created_at >= $1
		GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 10`, since)
	if err != nil {
		return nil, fmt.Errorf("count sources: %w", err)
	}
	sources, err := pgx.CollectRows(rows, pgx.RowToStructByPos[stats.SourceCount])
	if err != nil {
		return nil, fmt.Errorf("count sources: %w", err)
	}
	return sources, nil
}

// usage sums the LLM accounting between two calendar dates, inclusive.
func (r *StatsRepository) usage(ctx context.Context, from, to time.Time) (stats.Usage, error) {
	var (
		u       stats.Usage
		replyMS int64
	)
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(requests), 0), COALESCE(sum(failures), 0),
		       COALESCE(sum(prompt_tokens), 0), COALESCE(sum(cached_tokens), 0),
		       COALESCE(sum(completion_tokens), 0), COALESCE(sum(reply_ms), 0)
		FROM llm_usage WHERE day BETWEEN $1 AND $2`, from, to).
		Scan(&u.Replies, &u.Failures, &u.PromptTokens, &u.CachedTokens, &u.CompletionTokens, &replyMS)
	if err != nil {
		return stats.Usage{}, fmt.Errorf("sum llm usage: %w", err)
	}
	u.ReplyTime = time.Duration(replyMS) * time.Millisecond
	return u, nil
}
