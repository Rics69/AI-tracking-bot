// Package budget is the optional part of the money: goals, the pay plan,
// regular payments and the daily limit computed from them.
//
// The limit is computed by code, never by the LLM: it is a number the user
// acts on, so it must be exact. Nothing computed is stored; it is derived
// from the plan and the operations on every request.
package budget

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

const (
	MaxTitleLength = 100
	MaxGoals       = 20
	MaxPayments    = 50
	// DefaultReminderHour is when the daily reminder goes out, in the user's timezone.
	DefaultReminderHour = 9
)

var (
	// ErrNotFound: no such goal or regular payment among the user's own, or no plan.
	ErrNotFound = errors.New("not found")
	// NUMERIC(12,2) holds values below 10^10.
	maxAmount = decimal.New(1, 10)
)

// Goal is something the user saves for.
type Goal struct {
	ID     int64
	UserID int64
	Title  string
	Target decimal.Decimal
	// Saved is the sum of deposits, withdrawals subtracted; filled by the repository.
	Saved    decimal.Decimal
	Deadline *time.Time // calendar date; nil: no deadline
	// AchievedOn is the user's date when Saved first reached Target; it is
	// cleared if money is taken back below the target.
	AchievedOn *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Left is what remains to save; zero once the goal is reached.
func (g Goal) Left() decimal.Decimal {
	return decimal.Max(g.Target.Sub(g.Saved), decimal.Zero)
}

// Share is the saved part of the target, from 0 to 1 and above.
func (g Goal) Share() decimal.Decimal {
	if !g.Target.IsPositive() {
		return decimal.Zero
	}
	return g.Saved.Div(g.Target)
}

// Deposit puts money aside for a goal; a negative amount takes it back.
type Deposit struct {
	ID        int64
	GoalID    int64
	UserID    int64
	Amount    decimal.Decimal
	Date      time.Time
	CreatedAt time.Time
}

// Plan tells when money comes. Only the paydays are required.
type Plan struct {
	UserID int64
	// Paydays are one or two days of the month (1–31, ascending). A day past
	// the end of a short month means its last day.
	Paydays []int
	// ExpectedIncome of one pay period; nil: only recorded incomes count.
	ExpectedIncome *decimal.Decimal
	// GoalContribution is put aside for goals each period; nil: nothing planned.
	GoalContribution *decimal.Decimal
	ReminderHour     int  // 0–23, the user's timezone
	Reminders        bool // send the daily reminder
	UpdatedAt        time.Time
}

// RegularPayment is a monthly expense on a fixed day: rent, phone, a subscription.
type RegularPayment struct {
	ID       int64
	UserID   int64
	Title    string
	Amount   decimal.Decimal
	Day      int             // 1–31; past the end of a short month means its last day
	Category ledger.Category // an expense category
	// LastPaidOn is the date of the latest expense linked to the payment;
	// filled by the repository.
	LastPaidOn *time.Time
	CreatedAt  time.Time
}

func (p *Plan) normalize() error {
	p.Paydays = slices.Clone(p.Paydays)
	slices.Sort(p.Paydays)
	p.Paydays = slices.Compact(p.Paydays)
	if len(p.Paydays) == 0 || len(p.Paydays) > 2 {
		return invalid("paydays", "one or two days of the month")
	}
	for _, d := range p.Paydays {
		if d < 1 || d > 31 {
			return invalid("paydays", "a day of the month is from 1 to 31, got %d", d)
		}
	}
	if err := optionalAmount("expected_income", p.ExpectedIncome); err != nil {
		return err
	}
	if err := optionalAmount("goal_contribution", p.GoalContribution); err != nil {
		return err
	}
	if p.ReminderHour < 0 || p.ReminderHour > 23 {
		return invalid("reminder_hour", "must be from 0 to 23")
	}
	return nil
}

func (g *Goal) normalize(today time.Time) error {
	var err error
	if g.Title, err = title(g.Title); err != nil {
		return err
	}
	if err := checkAmount("target", g.Target); err != nil {
		return err
	}
	if g.Deadline != nil {
		d := dateOf(*g.Deadline)
		if d.Before(today) {
			return invalid("deadline", "must not be in the past")
		}
		g.Deadline = &d
	}
	return nil
}

func (p *RegularPayment) normalize() error {
	var err error
	if p.Title, err = title(p.Title); err != nil {
		return err
	}
	if err := checkAmount("amount", p.Amount); err != nil {
		return err
	}
	if p.Day < 1 || p.Day > 31 {
		return invalid("day", "a day of the month is from 1 to 31")
	}
	if p.Category == "" {
		p.Category = ledger.CategoryOther
	}
	if !p.Category.Valid() || p.Category.Kind() != ledger.KindExpense {
		return invalid("category", "a regular payment needs an expense category, got %q", p.Category)
	}
	return nil
}

func title(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid("title", "must not be empty")
	}
	if utf8.RuneCountInString(s) > MaxTitleLength {
		return "", invalid("title", "must be at most %d characters", MaxTitleLength)
	}
	return s, nil
}

func checkAmount(field string, d decimal.Decimal) error {
	switch {
	case !d.IsPositive():
		return invalid(field, "must be greater than zero")
	case !d.Equal(d.Round(2)):
		return invalid(field, "must have at most 2 decimal places")
	case d.GreaterThanOrEqual(maxAmount):
		return invalid(field, "is too large")
	}
	return nil
}

func optionalAmount(field string, d *decimal.Decimal) error {
	if d == nil {
		return nil
	}
	return checkAmount(field, *d)
}

// invalid reuses the ledger's validation error: the API and the agent
// already know how to report it.
func invalid(field, format string, args ...any) error {
	return &ledger.ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
