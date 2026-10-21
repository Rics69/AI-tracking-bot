package usage

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

type memStore map[int64]Day

func (m memStore) Add(_ context.Context, userID int64, _ time.Time, u llm.Usage, failed bool) error {
	d := m[userID]
	d.Requests++
	if failed {
		d.Failures++
	}
	d.PromptTokens += int64(u.PromptTokens)
	d.CachedTokens += int64(u.CachedTokens)
	d.CompletionTokens += int64(u.CompletionTokens)
	m[userID] = d
	return nil
}

func (m memStore) Get(_ context.Context, userID int64, _ time.Time) (Day, error) {
	return m[userID], nil
}

func TestLimits(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	m := NewMeter(memStore{}, Limits{Messages: 2, Tokens: 1000}, 99, slog.New(slog.NewTextHandler(io.Discard, nil)))
	allow := func(id int64) bool {
		t.Helper()
		ok, err := m.Allow(ctx, id, day)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	m.RecordUsage(ctx, 1, day, llm.Usage{PromptTokens: 100}, false)
	if !allow(1) {
		t.Error("one message of two: must be allowed")
	}
	m.RecordUsage(ctx, 1, day, llm.Usage{PromptTokens: 100}, true) // failures count too
	if allow(1) {
		t.Error("message limit reached: must be refused")
	}
	// Cached tokens are not billed and do not count.
	m.RecordUsage(ctx, 2, day, llm.Usage{PromptTokens: 900, CachedTokens: 5000, CompletionTokens: 99}, false)
	if !allow(2) {
		t.Error("999 billed tokens of 1000: must be allowed")
	}
	m.RecordUsage(ctx, 2, day, llm.Usage{CompletionTokens: 1}, false)
	if allow(2) {
		t.Error("token limit reached: must be refused")
	}
	for range 5 {
		m.RecordUsage(ctx, 99, day, llm.Usage{PromptTokens: 1000}, false)
	}
	if !allow(99) {
		t.Error("the owner is never limited")
	}
	if unlimited := NewMeter(memStore{1: {Requests: 1e6}}, Limits{}, 0, m.log); func() bool {
		ok, _ := unlimited.Allow(ctx, 1, day)
		return !ok
	}() {
		t.Error("zero limits mean unlimited")
	}
}
