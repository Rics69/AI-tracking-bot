// Package usage keeps per-user daily accounting of the LLM and enforces daily
// limits, so one user cannot spend the whole token budget.
package usage

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// Day is one user's usage on one calendar day (in their timezone).
type Day struct {
	Requests         int // agent replies, each possibly several model calls
	Failures         int
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	ReplyTime        time.Duration // total time of the replies
}

// Tokens is what counts against the limit: tokens the provider bills.
func (d Day) Tokens() int64 { return d.PromptTokens + d.CompletionTokens }

// Store persists daily usage.
type Store interface {
	Add(ctx context.Context, userID int64, day time.Time, u llm.Usage, failed bool, took time.Duration) error
	Get(ctx context.Context, userID int64, day time.Time) (Day, error)
}

// Limits per user per day; zero means unlimited.
type Limits struct {
	Messages int
	Tokens   int64
}

// Meter records usage and decides whether a user may ask the agent again today.
// It implements agent.UsageRecorder.
type Meter struct {
	store  Store
	limits Limits
	exempt int64 // the owner of the bot is not limited
	outage *outage
	now    func() time.Time
	log    *slog.Logger
}

func NewMeter(store Store, limits Limits, exempt int64, log *slog.Logger) *Meter {
	return &Meter{store: store, limits: limits, exempt: exempt, now: time.Now, log: log}
}

// outage watches failed replies across all users: many in a row mean the LLM is down.
type outage struct {
	mu     sync.Mutex
	after  int
	every  time.Duration
	notify func(ctx context.Context, failures int)
	streak int // failed replies in a row
	sent   time.Time
}

// OnOutage calls notify when after agent replies in a row have failed, at most
// once per every: the owner learns that the LLM is down before users complain.
// notify runs in its own goroutine and must not block the reply.
func (m *Meter) OnOutage(after int, every time.Duration, notify func(ctx context.Context, failures int)) {
	m.outage = &outage{after: after, every: every, notify: notify}
}

func (m *Meter) watch(ctx context.Context, failed bool) {
	o := m.outage
	if o == nil {
		return
	}
	o.mu.Lock()
	if !failed {
		o.streak = 0
		o.mu.Unlock()
		return
	}
	o.streak++
	now := m.now()
	fire := o.streak >= o.after && (o.sent.IsZero() || now.Sub(o.sent) >= o.every)
	if fire {
		o.sent = now
	}
	streak := o.streak
	o.mu.Unlock()
	if fire {
		go o.notify(context.WithoutCancel(ctx), streak)
	}
}

// Allow reports whether the user is within today's limits.
func (m *Meter) Allow(ctx context.Context, userID int64, day time.Time) (bool, error) {
	if userID == m.exempt || (m.limits.Messages == 0 && m.limits.Tokens == 0) {
		return true, nil
	}
	d, err := m.store.Get(ctx, userID, day)
	if err != nil {
		return false, err
	}
	if m.limits.Messages > 0 && d.Requests >= m.limits.Messages {
		return false, nil
	}
	if m.limits.Tokens > 0 && d.Tokens() >= m.limits.Tokens {
		return false, nil
	}
	return true, nil
}

// RecordUsage adds one agent reply to the user's day. A failure to save is
// logged: the user already has their answer.
func (m *Meter) RecordUsage(ctx context.Context, userID int64, day time.Time, u llm.Usage, failed bool, took time.Duration) {
	if err := m.store.Add(ctx, userID, day, u, failed, took); err != nil {
		m.log.ErrorContext(ctx, "record llm usage", "user_id", userID, "err", err)
	}
	m.watch(ctx, failed)
}
