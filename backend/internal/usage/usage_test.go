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

func (m memStore) Add(_ context.Context, userID int64, _ time.Time, u llm.Usage, failed bool, took time.Duration) error {
	d := m[userID]
	d.Requests++
	if failed {
		d.Failures++
	}
	d.PromptTokens += int64(u.PromptTokens)
	d.CachedTokens += int64(u.CachedTokens)
	d.CompletionTokens += int64(u.CompletionTokens)
	d.ReplyTime += took
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

	m.RecordUsage(ctx, 1, day, llm.Usage{PromptTokens: 100}, false, time.Second)
	if !allow(1) {
		t.Error("one message of two: must be allowed")
	}
	m.RecordUsage(ctx, 1, day, llm.Usage{PromptTokens: 100}, true, time.Second) // failures count too
	if allow(1) {
		t.Error("message limit reached: must be refused")
	}
	// Cached tokens are not billed and do not count.
	m.RecordUsage(ctx, 2, day, llm.Usage{PromptTokens: 900, CachedTokens: 5000, CompletionTokens: 99}, false, time.Second)
	if !allow(2) {
		t.Error("999 billed tokens of 1000: must be allowed")
	}
	m.RecordUsage(ctx, 2, day, llm.Usage{CompletionTokens: 1}, false, time.Second)
	if allow(2) {
		t.Error("token limit reached: must be refused")
	}
	for range 5 {
		m.RecordUsage(ctx, 99, day, llm.Usage{PromptTokens: 1000}, false, time.Second)
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

func TestOutageAlert(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	m := NewMeter(memStore{}, Limits{}, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.now = func() time.Time { return now }
	alerts := make(chan int, 10)
	m.OnOutage(3, time.Hour, func(_ context.Context, failures int) { alerts <- failures })
	reply := func(failed bool) { m.RecordUsage(ctx, 1, day, llm.Usage{}, failed, time.Second) }
	expect := func(want int) {
		t.Helper()
		select {
		case got := <-alerts:
			if got != want {
				t.Errorf("alert after %d failures, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no alert, want one after %d failures", want)
		}
	}
	quiet := func() {
		t.Helper()
		select {
		case got := <-alerts:
			t.Errorf("unexpected alert after %d failures", got)
		case <-time.After(50 * time.Millisecond):
		}
	}

	reply(true)
	reply(true)
	reply(false) // a success breaks the streak
	reply(true)
	reply(true)
	quiet()
	reply(true)
	expect(3)
	reply(true) // still down, but alerted less than an hour ago
	quiet()
	now = now.Add(time.Hour)
	reply(true)
	expect(5)
}
