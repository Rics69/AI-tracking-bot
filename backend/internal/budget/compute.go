package budget

import (
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// Budget is the money of the current pay period as of today.
type Budget struct {
	Today       time.Time
	PeriodStart time.Time // the last payday, today included
	NextPayday  time.Time
	DaysLeft    int // days until the next payday, today included

	// Income of the period: what is recorded, or the expected income while
	// less than that is recorded.
	Income         decimal.Decimal
	IncomeExpected bool
	// Payments are the regular payments of the period, paid or still due.
	Payments decimal.Decimal
	// Savings is put aside for goals: as planned, or more if more was deposited.
	Savings decimal.Decimal
	// Spent before today and SpentToday leave regular payments aside: those
	// are already in Payments.
	Spent      decimal.Decimal
	SpentToday decimal.Decimal

	// Available is what is left for the rest of the period at the start of
	// today. The limit is computed from it, so overspending today lowers the
	// limit of the following days, not of today.
	Available decimal.Decimal
	// Limit is what may be spent today: Available spread evenly over DaysLeft,
	// in whole rubles; zero when nothing is available.
	Limit decimal.Decimal
	// Ready is false when no income is recorded or expected: the limit is unknown.
	Ready bool
	// Upcoming are the regular payments of the period not paid yet, by date.
	Upcoming []Due
}

// Due is a regular payment expected on a date.
type Due struct {
	Payment RegularPayment
	Date    time.Time
}

// Left is what may still be spent today; negative when the limit is exceeded.
func (b Budget) Left() decimal.Decimal { return b.Limit.Sub(b.SpentToday) }

// Remaining is what is left until the next payday after today's expenses.
func (b Budget) Remaining() decimal.Decimal { return b.Available.Sub(b.SpentToday) }

// Overspent reports whether today's expenses exceed the limit.
func (b Budget) Overspent() bool { return b.Ready && b.SpentToday.GreaterThan(b.Limit) }

// Input is what the limit is computed from.
type Input struct {
	Today    time.Time
	Plan     Plan
	Payments []RegularPayment
	// Operations are the user's own, at least from the period start to today;
	// others are ignored.
	Operations []ledger.Operation
	// Saved is the net amount deposited to goals in the period.
	Saved decimal.Decimal
}

// Compute derives the budget of the current pay period.
func Compute(in Input) Budget {
	start, next := Period(in.Today, in.Plan.Paydays)
	b := Budget{
		Today: in.Today, PeriodStart: start, NextPayday: next, DaysLeft: DaysLeft(in.Today, next),
		Income: decimal.Zero, Payments: decimal.Zero, Spent: decimal.Zero, SpentToday: decimal.Zero,
		Available: decimal.Zero, Limit: decimal.Zero,
	}

	known := map[int64]bool{}
	for _, p := range in.Payments {
		known[p.ID] = true
	}
	paid := map[int64]decimal.Decimal{}
	for _, op := range in.Operations {
		if op.Date.Before(start) || op.Date.After(in.Today) {
			continue
		}
		switch {
		case op.Kind() == ledger.KindIncome:
			b.Income = b.Income.Add(op.Amount)
		case op.RegularPaymentID != nil && known[*op.RegularPaymentID]:
			paid[*op.RegularPaymentID] = paid[*op.RegularPaymentID].Add(op.Amount)
		case op.Date.Equal(in.Today):
			b.SpentToday = b.SpentToday.Add(op.Amount)
		default:
			b.Spent = b.Spent.Add(op.Amount)
		}
	}
	if e := in.Plan.ExpectedIncome; e != nil && e.GreaterThan(b.Income) {
		b.Income, b.IncomeExpected = *e, true
	}

	for _, p := range in.Payments {
		// A payment added mid-period is not owed for the dates before it.
		dates := occurrences(p.Day, maxDate(start, dateOf(p.CreatedAt)), next)
		owed := p.Amount.Mul(decimal.NewFromInt(int64(len(dates))))
		b.Payments = b.Payments.Add(decimal.Max(owed, paid[p.ID]))
		for i, d := range dates {
			// Paid amounts cover the dates in order.
			if paid[p.ID].LessThan(p.Amount.Mul(decimal.NewFromInt(int64(i + 1)))) {
				b.Upcoming = append(b.Upcoming, Due{Payment: p, Date: d})
			}
		}
	}
	slices.SortStableFunc(b.Upcoming, func(x, y Due) int { return x.Date.Compare(y.Date) })

	b.Savings = decimal.Max(in.Saved, decimal.Zero)
	if c := in.Plan.GoalContribution; c != nil {
		b.Savings = decimal.Max(b.Savings, *c)
	}

	b.Available = b.Income.Sub(b.Payments).Sub(b.Savings).Sub(b.Spent)
	b.Ready = b.Income.IsPositive()
	if b.Ready && b.Available.IsPositive() {
		b.Limit = b.Available.Div(decimal.NewFromInt(int64(b.DaysLeft))).RoundFloor(0)
	}
	return b
}

func maxDate(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
