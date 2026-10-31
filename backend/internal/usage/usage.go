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
	// Total sums the tokens the provider bills, of everyone, from a calendar date on.
	Total(ctx context.Context, from time.Time) (int64, error)
}

// Limits per user per day; zero means unlimited.
type Limits struct {
	Messages int
	Tokens   int64
}

// Package is the supply of tokens shared by all users; zero values mean no
// such limit. Cached tokens do not count against it, as with the daily limits.
type Package struct {
	Tokens int64     // bought or given once
	Since  time.Time // the calendar date it started; earlier usage is not its own
	Daily  int64     // a ceiling per day, so a burst of new users does not eat it all at once
}

// exhaustedTTL is how long the answer of Exhausted is reused: it is asked on
// every message that goes to the agent.
const exhaustedTTL = time.Minute

// Meter records usage and decides whether a user may ask the agent again today.
// It implements agent.UsageRecorder.
type Meter struct {
	store  Store
	limits Limits
	exempt int64 // the owner of the bot is not limited
	outage *outage
	now    func() time.Time
	log    *slog.Logger

	pack Package
	// The last answer of Exhausted.
	mu        sync.Mutex
	checked   time.Time
	checkedOn time.Time
	exhausted bool
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

// SetPackage turns on the shared supply of tokens.
func (m *Meter) SetPackage(p Package) { m.pack = p }

// Spent is how many tokens of the package are gone.
func (m *Meter) Spent(ctx context.Context) (int64, error) {
	return m.store.Total(ctx, m.pack.Since)
}

// Exhausted reports that the package or the day's ceiling is used up: the
// agent is off for everyone, the owner included, until there are tokens again.
func (m *Meter) Exhausted(ctx context.Context, day time.Time) (bool, error) {
	if m.pack.Tokens == 0 && m.pack.Daily == 0 {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if !m.checked.IsZero() && now.Sub(m.checked) < exhaustedTTL && day.Equal(m.checkedOn) {
		return m.exhausted, nil
	}
	exhausted, err := m.spentAll(ctx, day)
	if err != nil {
		return false, err
	}
	m.checked, m.checkedOn, m.exhausted = now, day, exhausted
	return exhausted, nil
}

func (m *Meter) spentAll(ctx context.Context, day time.Time) (bool, error) {
	if m.pack.Tokens > 0 {
		spent, err := m.Spent(ctx)
		if err != nil {
			return false, err
		}
		if spent >= m.pack.Tokens {
			return true, nil
		}
	}
	if m.pack.Daily > 0 {
		spent, err := m.store.Total(ctx, day)
		if err != nil {
			return false, err
		}
		return spent >= m.pack.Daily, nil
	}
	return false, nil
}

// RecordUsage adds one agent reply to the user's day. A failure to save is
// logged: the user already has their answer.
func (m *Meter) RecordUsage(ctx context.Context, userID int64, day time.Time, u llm.Usage, failed bool, took time.Duration) {
	if err := m.store.Add(ctx, userID, day, u, failed, took); err != nil {
		m.log.ErrorContext(ctx, "record llm usage", "user_id", userID, "err", err)
	}
	m.watch(ctx, failed)
}
