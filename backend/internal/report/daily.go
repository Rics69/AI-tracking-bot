package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/savik"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
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

// Rates are the exchange rates of the morning message; *postgres.RateRepository
// implements it.
type Rates interface {
	Latest(ctx context.Context, codes []string) ([]rates.Rate, error)
}

// MorningExtras is what other parts add to the morning message.
type MorningExtras interface {
	Reminded(ctx context.Context) ([]int64, error)
	Morning(ctx context.Context, m user.User, today time.Time) (MorningExtra, error)
}

// MorningTasks is what is planned; *tasks.Service implements it.
type MorningTasks interface {
	Users(ctx context.Context, day time.Time) ([]int64, error)
	Today(ctx context.Context, userID int64, day time.Time) ([]tasks.Task, error)
}

// Daily sends the morning message: today's limit for those who set a payday,
// the main goal, regular payments due tomorrow and exchange rates. Like the
// weekly report, the text is built by code from a phrase bank, so the numbers
// are exact and free.
type Daily struct {
	budgets Budgets
	members Members
	runs    Runs
	rates   Rates    // nil: no rates
	send    SendFunc // delivers with a button that opens the mini app
	// Extras adds what the game has for the morning, and SendRows delivers it
	// with its buttons; both optional.
	Extras   MorningExtras
	SendRows ActionSend
	// Tasks adds what is planned for the day, Debts the payments coming up; optional.
	Tasks MorningTasks
	Debts MorningDebts
	t     texts.Bot
	loc   *time.Location // timezone of users who have not chosen one
	now   func() time.Time
	pick  func(n int) int // random phrase index
	log   *slog.Logger
}

func NewDaily(budgets Budgets, members Members, runs Runs, r Rates, send SendFunc, t texts.Bot, loc *time.Location, log *slog.Logger) *Daily {
	return &Daily{budgets: budgets, members: members, runs: runs, rates: r, send: send, t: t, loc: loc,
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
	if d.Extras != nil {
		more, err := d.Extras.Reminded(ctx)
		if err != nil {
			d.log.ErrorContext(ctx, "daily reminder: list challenge users", "err", err)
		}
		ids = append(ids, more...)
	}
	if d.Tasks != nil {
		more, err := d.Tasks.Users(ctx, dateOfTime(d.now().In(d.loc)))
		if err != nil {
			d.log.ErrorContext(ctx, "daily reminder: list task users", "err", err)
		}
		ids = append(ids, more...)
	}
	if d.Debts != nil {
		more, err := d.Debts.Users(ctx)
		if err != nil {
			d.log.ErrorContext(ctx, "daily reminder: list debt users", "err", err)
		}
		ids = append(ids, more...)
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

// Pauses of the morning message for those who stopped coming.
const (
	sparseAfterDays = 7 // from here on, once in sparseEveryDays
	sparseEveryDays = 3
	silentAfterDays = 30 // one last message, then silence
)

// kindComeback is the Runs kind of the last message to someone who left; the
// period is the day they were last seen, so a new absence gets a new message.
const kindComeback = "comeback"

func (d *Daily) sendOne(ctx context.Context, m user.User, now time.Time) error {
	if !m.Reminders || now.Hour() < m.ReminderHour {
		return nil
	}
	today := dateOfTime(now)
	idle := 0
	if m.LastSeenAt != nil {
		idle = int(today.Sub(dateOfTime(m.LastSeenAt.In(now.Location()))).Hours() / 24)
	}
	switch {
	case idle >= silentAfterDays:
		return d.comeback(ctx, m, today, m.LastSeenAt.In(now.Location()))
	case idle >= sparseAfterDays && (idle-sparseAfterDays)%sparseEveryDays != 0:
		return nil
	}
	period := today.Format(time.DateOnly)
	claimed, err := d.runs.Claim(ctx, kindDaily, period, m.TelegramID)
	if err != nil || !claimed {
		return err
	}
	text, rows, err := d.Build(ctx, m, today)
	if err == nil && text == "" {
		return nil // nothing to say today: stay quiet, but mark the day as handled
	}
	return d.deliver(ctx, kindDaily, period, m.TelegramID, text, rows, err)
}

// comeback says goodbye once and leaves the user alone.
func (d *Daily) comeback(ctx context.Context, m user.User, today, lastSeen time.Time) error {
	period := lastSeen.Format(time.DateOnly)
	claimed, err := d.runs.Claim(ctx, kindComeback, period, m.TelegramID)
	if err != nil || !claimed {
		return err
	}
	goals, err := d.budgets.Goals(ctx, m.TelegramID)
	text := d.t.Comeback
	if g := budget.MainGoal(goals); g != nil {
		text = texts.Fill(d.t.ComebackGoal, "title", g.Title, "percent", strconv.Itoa(percent(*g)))
	}
	return d.deliver(ctx, kindComeback, period, m.TelegramID, text, nil, err)
}

// deliver sends what was claimed, and gives the claim back if it failed.
func (d *Daily) deliver(ctx context.Context, kind, period string, userID int64, text string, rows [][]game.Action, err error) error {
	switch {
	case err != nil:
	case len(rows) > 0 && d.SendRows != nil:
		err = d.SendRows(ctx, userID, text, rows)
	default:
		err = d.send(ctx, userID, text)
	}
	if errors.Is(err, ErrUndeliverable) {
		d.log.WarnContext(ctx, "daily reminder undeliverable", "user_id", userID, "err", err)
		return nil
	}
	if err != nil {
		if relErr := d.runs.Release(ctx, kind, period, userID); relErr != nil {
			d.log.ErrorContext(ctx, "daily reminder: release claim", "err", relErr)
		}
		return err
	}
	d.log.InfoContext(ctx, "daily reminder sent", "user_id", userID, "kind", kind, "period", period)
	return nil
}

// Build renders the message of a day; empty when there is nothing to say.
// Rates alone are not a reason to write.
func (d *Daily) Build(ctx context.Context, m user.User, today time.Time) (string, [][]game.Action, error) {
	userID := m.TelegramID
	plan, err := d.budgets.Plan(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	goals, err := d.budgets.Goals(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	var lines []string
	if plan != nil {
		b, err := d.budgets.Budget(ctx, userID, today)
		if err != nil {
			return "", nil, err
		}
		// In the morning today's spending is usually zero: it counts as over
		// the limit when yesterday went over the limit yesterday had.
		yesterday, err := d.budgets.Budget(ctx, userID, today.AddDate(0, 0, -1))
		if err != nil {
			return "", nil, err
		}
		lines = append(lines, d.limitLine(b, yesterday != nil && yesterday.Overspent(), goals, today))
	}
	if g := budget.MainGoal(goals); g != nil {
		lines = append(lines, goalLine(d.t.ReminderGoal, *g))
	}
	payments, err := d.budgets.Payments(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	tomorrow := today.AddDate(0, 0, 1)
	for _, p := range payments {
		if p.DueOn(tomorrow) {
			lines = append(lines, texts.Fill(d.t.ReminderPayment, "title", p.Title, "amount", Money(p.Amount)))
		}
	}
	if d.Tasks != nil {
		list, err := d.Tasks.Today(ctx, userID, today)
		if err != nil {
			d.log.WarnContext(ctx, "daily reminder: tasks", "user_id", userID, "err", err)
		}
		if len(list) > 0 {
			lines = append(lines, taskLine(d.t, list))
		}
	}
	if d.Debts != nil {
		list, err := d.Debts.DueOn(ctx, userID, today.AddDate(0, 0, debtNoticeDays))
		if err != nil {
			d.log.WarnContext(ctx, "daily reminder: debts", "user_id", userID, "err", err)
		}
		if len(list) > 0 {
			lines = append(lines, debtLine(d.t, list))
		}
	}
	var extra MorningExtra
	if d.Extras != nil {
		if extra, err = d.Extras.Morning(ctx, m, today); err != nil {
			d.log.WarnContext(ctx, "daily reminder: game", "user_id", userID, "err", err) // the money part still goes out
		}
	}
	if len(lines) == 0 && !extra.Alone {
		return "", nil, nil
	}
	lines = append(lines, extra.Lines...)
	if m.ShowRates {
		if line := d.ratesLine(ctx); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), extra.Rows, nil
}

// ratesLine is empty when there are no rates: the message goes out without them.
func (d *Daily) ratesLine(ctx context.Context) string {
	if d.rates == nil {
		return ""
	}
	items, err := d.rates.Latest(ctx, rates.Codes)
	if err != nil {
		d.log.WarnContext(ctx, "daily reminder: exchange rates", "err", err)
		return ""
	}
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	newest := items[0].Date
	for _, code := range rates.Codes {
		for _, r := range items {
			if r.Code == code {
				parts = append(parts, currencySigns[code]+" "+Money(r.Value.Round(2)))
				if r.Date.After(newest) {
					newest = r.Date
				}
			}
		}
	}
	return texts.Fill(d.t.ReminderRates, "rates", strings.Join(parts, " · "), "date", dayMonth(newest))
}

var currencySigns = map[string]string{"USD": "$", "EUR": "€"}

func goalLine(text string, g budget.Goal) string {
	return texts.Fill(text, "title", g.Title, "percent", strconv.Itoa(percent(g)),
		"saved", Money(g.Saved), "target", Money(g.Target))
}

// percent is the saved part of a goal in whole percent, rounded down: 100
// means the money is really all there.
func percent(g budget.Goal) int {
	return int(g.Share().Mul(decimal.NewFromInt(100)).IntPart())
}

func dateOfTime(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
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
