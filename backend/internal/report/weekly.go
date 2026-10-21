// Package report sends every user a summary of their past week.
//
// The text is built by code, not by the LLM: numbers in a report must be
// exact, and it costs no tokens.
package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const (
	// SendHour is when Monday reports go out, in each user's timezone.
	SendHour     = 10
	tickInterval = 5 * time.Minute
	topExpenses  = 3
)

// Expenses is the part of expense.Service a report needs.
type Expenses interface {
	List(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Page, error)
	Summary(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Summary, error)
}

// Members lists everyone the bot knows.
type Members interface {
	All(ctx context.Context) ([]user.User, error)
}

// Runs records handled reports so each is sent once.
type Runs interface {
	Claim(ctx context.Context, weekStart time.Time, userID int64) (bool, error)
	Release(ctx context.Context, weekStart time.Time, userID int64) error
}

// SendFunc delivers a message to a member's private chat. It returns an error
// wrapping ErrUndeliverable when retrying cannot help (the user blocked the bot).
type SendFunc func(ctx context.Context, chatID int64, text string) error

// ErrUndeliverable marks a permanent delivery failure: the report is skipped,
// not retried every few minutes.
var ErrUndeliverable = errors.New("report cannot be delivered")

// Weekly sends weekly reports on schedule.
type Weekly struct {
	expenses Expenses
	members  Members
	runs     Runs
	send     SendFunc
	loc      *time.Location // timezone of users who have not chosen one
	now      func() time.Time
	log      *slog.Logger
}

func NewWeekly(expenses Expenses, members Members, runs Runs, send SendFunc, loc *time.Location, log *slog.Logger) *Weekly {
	return &Weekly{expenses: expenses, members: members, runs: runs, send: send, loc: loc, now: time.Now, log: log}
}

// Run checks every few minutes whether reports are due, until ctx is cancelled.
// Reports for the past week go out from Monday 10:00 in the user's timezone;
// if the bot was down then, they are sent when it is back, up to the end of the week.
func (w *Weekly) Run(ctx context.Context) {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick sends all reports that are due and not yet handled.
func (w *Weekly) Tick(ctx context.Context) {
	members, err := w.members.All(ctx)
	if err != nil {
		w.log.ErrorContext(ctx, "weekly report: list users", "err", err)
		return
	}
	now := w.now()
	for _, m := range members {
		if !m.WeeklyReport || !m.Consented() || m.Blocked() {
			continue
		}
		weekStart, due := reportWeek(now.In(m.Location(w.loc)))
		if !due {
			continue
		}
		if err := w.sendOne(ctx, m, weekStart); err != nil {
			w.log.ErrorContext(ctx, "weekly report", "user_id", m.TelegramID, "err", err)
		}
	}
}

func (w *Weekly) sendOne(ctx context.Context, m user.User, weekStart time.Time) error {
	claimed, err := w.runs.Claim(ctx, weekStart, m.TelegramID)
	if err != nil || !claimed {
		return err
	}
	text, empty, err := w.Build(ctx, m.TelegramID, weekStart)
	if err == nil && empty {
		return nil // nothing to report: stay quiet, but mark the week as handled
	}
	if err == nil {
		err = w.send(ctx, m.TelegramID, text)
	}
	if errors.Is(err, ErrUndeliverable) {
		w.log.WarnContext(ctx, "weekly report undeliverable", "user_id", m.TelegramID, "err", err)
		return nil // keep the claim: the member will not get this week's report
	}
	if err != nil {
		// Let the next tick retry.
		if relErr := w.runs.Release(ctx, weekStart, m.TelegramID); relErr != nil {
			w.log.ErrorContext(ctx, "weekly report: release claim", "err", relErr)
		}
		return err
	}
	w.log.InfoContext(ctx, "weekly report sent", "user_id", m.TelegramID, "week", weekStart.Format(time.DateOnly))
	return nil
}

// reportWeek returns the Monday of the week to report on (the previous
// calendar week) and whether its report is due at now.
func reportWeek(now time.Time) (weekStart time.Time, due bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	due = !today.Equal(monday) || now.Hour() >= SendHour
	return monday.AddDate(0, 0, -7), due
}

// Build renders the report of the week starting at weekStart for a member.
// empty is true when there were no expenses that week.
func (w *Weekly) Build(ctx context.Context, userID int64, weekStart time.Time) (text string, empty bool, err error) {
	scope := expense.OwnedBy(userID)
	weekEnd := weekStart.AddDate(0, 0, 6)
	prevStart, prevEnd := weekStart.AddDate(0, 0, -7), weekStart.AddDate(0, 0, -1)

	cur, err := w.expenses.Summary(ctx, scope, expense.Filter{From: &weekStart, To: &weekEnd})
	if err != nil {
		return "", false, err
	}
	if cur.Count == 0 {
		return "", true, nil
	}
	prev, err := w.expenses.Summary(ctx, scope, expense.Filter{From: &prevStart, To: &prevEnd})
	if err != nil {
		return "", false, err
	}
	top, err := w.expenses.List(ctx, scope, expense.Filter{
		From: &weekStart, To: &weekEnd, Sort: expense.SortByAmount, Limit: topExpenses,
	})
	if err != nil {
		return "", false, err
	}
	return render(weekStart, cur, prev, top.Items), false, nil
}

func render(weekStart time.Time, cur, prev expense.Summary, top []expense.Expense) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Итоги недели · %s\n\n", weekRange(weekStart))
	fmt.Fprintf(&b, "Потрачено %s — %d %s", Money(cur.Sum), cur.Count, plural(cur.Count, "трата", "траты", "трат"))
	if prev.Count > 0 {
		fmt.Fprintf(&b, "\n%s", comparison(cur.Sum, prev.Sum))
	}
	b.WriteString("\n\nПо категориям:")
	for _, c := range cur.Items {
		fmt.Fprintf(&b, "\n• %s — %s", c.Category.Label(), Money(c.Sum))
	}
	if len(top) > 1 {
		b.WriteString("\n\nСамые крупные:")
		for _, e := range top {
			what := e.Comment
			if what == "" {
				what = e.Category.Label()
			}
			fmt.Fprintf(&b, "\n• %s — %s, %s", what, Money(e.Amount), dayMonth(e.SpentAt))
		}
	}
	b.WriteString("\n\nОтключить итоги — /settings")
	return b.String()
}

func comparison(cur, prev decimal.Decimal) string {
	if prev.IsZero() {
		return ""
	}
	// "На 1380% больше" is hard to read: from twice as much, say "в 14,8 раза".
	if ratio := cur.Div(prev); ratio.GreaterThanOrEqual(decimal.NewFromInt(2)) {
		r := ratio.Round(1)
		times := "раза"
		if r.IsInteger() {
			times = plural(int(r.IntPart()), "раз", "раза", "раз")
		}
		return fmt.Sprintf("В %s %s больше, чем неделей раньше (%s).",
			strings.Replace(r.String(), ".", ",", 1), times, Money(prev))
	}
	diff := cur.Sub(prev).Div(prev).Mul(decimal.NewFromInt(100)).Round(0).IntPart()
	switch {
	case diff > 0:
		return fmt.Sprintf("На %d%% больше, чем неделей раньше (%s).", diff, Money(prev))
	case diff < 0:
		return fmt.Sprintf("На %d%% меньше, чем неделей раньше (%s).", -diff, Money(prev))
	default:
		return fmt.Sprintf("Столько же, сколько неделей раньше (%s).", Money(prev))
	}
}

var monthsGenitive = [...]string{"", "января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

func dayMonth(d time.Time) string { return fmt.Sprintf("%d %s", d.Day(), monthsGenitive[d.Month()]) }

// weekRange: "6–12 октября" or "29 сентября – 5 октября".
func weekRange(start time.Time) string {
	end := start.AddDate(0, 0, 6)
	if start.Month() == end.Month() {
		return fmt.Sprintf("%d–%d %s", start.Day(), end.Day(), monthsGenitive[end.Month()])
	}
	return dayMonth(start) + " – " + dayMonth(end)
}

// Money formats an amount the Russian way: "12 450 ₽", "99,90 ₽".
func Money(d decimal.Decimal) string {
	whole := d.Truncate(0)
	digits := whole.Abs().String()
	var b strings.Builder
	if d.IsNegative() {
		b.WriteString("-")
	}
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteRune(' ') // non-breaking space keeps the number on one line
		}
		b.WriteRune(r)
	}
	if frac := d.Sub(whole).Abs(); !frac.IsZero() {
		b.WriteString("," + frac.StringFixed(2)[2:])
	}
	b.WriteString(" ₽")
	return b.String()
}

func plural(n int, one, few, many string) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return few
	default:
		return many
	}
}
