package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/savik"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// kindDaily is the Runs kind of morning reminders; the period is the user's date.
const kindDaily = "daily"

// Budgets is the part of budget.Service a reminder needs.
type Budgets interface {
	Reminded(ctx context.Context) ([]int64, error)
	Plan(ctx context.Context, userID int64) (*budget.Plan, error)
	Budget(ctx context.Context, userID int64, today time.Time) (*budget.Budget, error)
	Goals(ctx context.Context, userID int64) ([]budget.Goal, error)
	Payments(ctx context.Context, userID int64) ([]budget.RegularPayment, error)
}

// Daily sends the morning reminder: today's limit for those who set a payday,
// and regular payments due tomorrow. Like the weekly report, the text is
// built by code from a phrase bank, so the numbers are exact and free.
type Daily struct {
	budgets Budgets
	members Members
	runs    Runs
	send    SendFunc // delivers with a button that opens the mini app
	t       texts.Bot
	loc     *time.Location // timezone of users who have not chosen one
	now     func() time.Time
	pick    func(n int) int // random phrase index
	log     *slog.Logger
}

func NewDaily(budgets Budgets, members Members, runs Runs, send SendFunc, t texts.Bot, loc *time.Location, log *slog.Logger) *Daily {
	return &Daily{budgets: budgets, members: members, runs: runs, send: send, t: t, loc: loc,
		now: time.Now, pick: rand.IntN, log: log}
}

// Run checks every few minutes whether reminders are due, until ctx is cancelled.
func (d *Daily) Run(ctx context.Context) {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		d.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick sends the reminders that are due today and not yet handled. A reminder
// missed while the bot was down goes out when it is back, the same day.
func (d *Daily) Tick(ctx context.Context) {
	ids, err := d.budgets.Reminded(ctx)
	if err != nil {
		d.log.ErrorContext(ctx, "daily reminder: list users", "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	reminded := make(map[int64]bool, len(ids))
	for _, id := range ids {
		reminded[id] = true
	}
	members, err := d.members.All(ctx)
	if err != nil {
		d.log.ErrorContext(ctx, "daily reminder: list users", "err", err)
		return
	}
	now := d.now()
	for _, m := range members {
		if !reminded[m.TelegramID] || !m.Consented() || m.Blocked() {
			continue
		}
		if err := d.sendOne(ctx, m, now.In(m.Location(d.loc))); err != nil {
			d.log.ErrorContext(ctx, "daily reminder", "user_id", m.TelegramID, "err", err)
		}
	}
}

func (d *Daily) sendOne(ctx context.Context, m user.User, now time.Time) error {
	plan, err := d.budgets.Plan(ctx, m.TelegramID)
	if err != nil {
		return err
	}
	hour := budget.DefaultReminderHour
	if plan != nil {
		if !plan.Reminders {
			return nil // turned off: no reminders at all
		}
		hour = plan.ReminderHour
	}
	if now.Hour() < hour {
		return nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	period := today.Format(time.DateOnly)
	claimed, err := d.runs.Claim(ctx, kindDaily, period, m.TelegramID)
	if err != nil || !claimed {
		return err
	}
	text, err := d.Build(ctx, m.TelegramID, plan, today)
	if err == nil && text == "" {
		return nil // nothing to say today: stay quiet, but mark the day as handled
	}
	if err == nil {
		err = d.send(ctx, m.TelegramID, text)
	}
	if errors.Is(err, ErrUndeliverable) {
		d.log.WarnContext(ctx, "daily reminder undeliverable", "user_id", m.TelegramID, "err", err)
		return nil
	}
	if err != nil {
		if relErr := d.runs.Release(ctx, kindDaily, period, m.TelegramID); relErr != nil {
			d.log.ErrorContext(ctx, "daily reminder: release claim", "err", relErr)
		}
		return err
	}
	d.log.InfoContext(ctx, "daily reminder sent", "user_id", m.TelegramID, "day", period)
	return nil
}

// Build renders the reminder of a day; empty when there is nothing to remind.
func (d *Daily) Build(ctx context.Context, userID int64, plan *budget.Plan, today time.Time) (string, error) {
	var lines []string
	if plan != nil {
		b, err := d.budgets.Budget(ctx, userID, today)
		if err != nil {
			return "", err
		}
		goals, err := d.budgets.Goals(ctx, userID)
		if err != nil {
			return "", err
		}
		// In the morning today's spending is usually zero: Savik is hungry
		// when yesterday went over the limit yesterday had.
		yesterday, err := d.budgets.Budget(ctx, userID, today.AddDate(0, 0, -1))
		if err != nil {
			return "", err
		}
		lines = append(lines, d.limitLine(b, yesterday != nil && yesterday.Overspent(), goals, today))
	}
	payments, err := d.budgets.Payments(ctx, userID)
	if err != nil {
		return "", err
	}
	tomorrow := today.AddDate(0, 0, 1)
	for _, p := range payments {
		if p.DueOn(tomorrow) {
			lines = append(lines, texts.Fill(d.t.ReminderPayment, "title", p.Title, "amount", Money(p.Amount)))
		}
	}
	return strings.Join(lines, "\n\n"), nil
}

func (d *Daily) limitLine(b *budget.Budget, overspentYesterday bool, goals []budget.Goal, today time.Time) string {
	if !b.Ready {
		return d.t.ReminderNoIncome
	}
	state := savik.Of(savik.Input{Today: today, Month: ledger.Summary{}, Budget: b, Goals: goals})
	if overspentYesterday && (state == savik.Neutral || state == savik.Fed) {
		state = savik.Hungry // the same priority as overspending today
	}
	phrases := d.phrases(state)
	return texts.Fill(phrases[d.pick(len(phrases))],
		"limit", Money(b.Limit),
		"left", Money(b.Remaining()),
		"days", fmt.Sprintf("%d %s", b.DaysLeft, plural(b.DaysLeft, "день", "дня", "дней")),
		"payday", dayMonth(b.NextPayday))
}

func (d *Daily) phrases(s savik.State) []string {
	switch s {
	case savik.Full:
		return d.t.Reminders.Full
	case savik.Empty:
		return d.t.Reminders.Empty
	case savik.Hungry:
		return d.t.Reminders.Hungry
	case savik.Fed:
		return d.t.Reminders.Fed
	default:
		return d.t.Reminders.Neutral
	}
}
