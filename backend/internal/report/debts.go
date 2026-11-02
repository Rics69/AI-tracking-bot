package report

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// debtNoticeDays: the morning line comes this long before a payment.
const debtNoticeDays = 3

// MorningDebts is what the morning message says about debts; *debts.Service
// implements it.
type MorningDebts interface {
	Users(ctx context.Context) ([]int64, error)
	DueOn(ctx context.Context, userID int64, date time.Time) ([]debts.Debt, error)
}

func debtLine(t texts.Bot, list []debts.Debt) string {
	lines := make([]string, len(list))
	for i, d := range list {
		lines[i] = texts.Fill(t.ReminderDebt, "name", d.Name, "amount", Money(d.MinPayment), "days", strconv.Itoa(debtNoticeDays))
	}
	return strings.Join(lines, "\n")
}

// DebtWatch is what the capital loop needs; *debts.Service implements it.
type DebtWatch interface {
	ClosedUsers(ctx context.Context) ([]int64, error)
	Snap(ctx context.Context, userID int64, today time.Time) error
}

// DebtRewards gives the award for the first closed debt; *game.Service implements it.
type DebtRewards interface {
	RewardDebt(ctx context.Context, userID int64) (int, bool, error)
}

// Capital marks the first closed debt and keeps the monthly net worth. It is
// a loop for the same reason as the rest: payments also come through the API.
type Capital struct {
	debts   DebtWatch
	rewards DebtRewards
	members Members
	send    SendFunc
	t       texts.Bot
	loc     *time.Location
	now     func() time.Time
	log     *slog.Logger

	mu      sync.Mutex
	snapped map[int64]time.Time
}

func NewCapital(d DebtWatch, rewards DebtRewards, members Members, send SendFunc, t texts.Bot, loc *time.Location, log *slog.Logger) *Capital {
	return &Capital{debts: d, rewards: rewards, members: members, send: send, t: t, loc: loc, now: time.Now, log: log,
		snapped: map[int64]time.Time{}}
}

// Tick runs both. Snapshots are refreshed once a day per user: the last one of
// a month stays as its result.
func (c *Capital) Tick(ctx context.Context) {
	closed, err := c.debts.ClosedUsers(ctx)
	if err != nil {
		c.log.ErrorContext(ctx, "capital: closed debts", "err", err)
	}
	for _, id := range closed {
		xp, first, err := c.rewards.RewardDebt(ctx, id)
		if err != nil {
			c.log.ErrorContext(ctx, "capital: reward", "user_id", id, "err", err)
			continue
		}
		if first {
			if err := c.send(ctx, id, texts.Fill(c.t.DebtFirstClosed, "xp", strconv.Itoa(xp))); err != nil {
				c.log.WarnContext(ctx, "capital: first debt", "user_id", id, "err", err)
			}
		}
	}
	members, err := c.members.All(ctx)
	if err != nil {
		c.log.ErrorContext(ctx, "capital: list users", "err", err)
		return
	}
	now := c.now()
	for _, m := range members {
		if m.Consented() && !m.Blocked() {
			c.snap(ctx, m, now)
		}
	}
}

func (c *Capital) snap(ctx context.Context, m user.User, now time.Time) {
	day := dateOfTime(now.In(m.Location(c.loc)))
	c.mu.Lock()
	done := c.snapped[m.TelegramID].Equal(day)
	c.snapped[m.TelegramID] = day
	c.mu.Unlock()
	if done {
		return
	}
	if err := c.debts.Snap(ctx, m.TelegramID, day); err != nil {
		c.log.ErrorContext(ctx, "capital: snapshot", "user_id", m.TelegramID, "err", err)
	}
}
