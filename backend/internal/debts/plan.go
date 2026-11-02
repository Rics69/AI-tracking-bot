package debts

import (
	"cmp"
	"errors"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// Ways to pay debts off.
const (
	Avalanche = "avalanche" // the highest rate first: the least interest
	Snowball  = "snowball"  // the smallest balance first: the quickest first win
)

// maxPlanMonths stops a plan that would take a lifetime.
const maxPlanMonths = 600

// ErrNeverPaid: the payments do not outrun the interest.
var ErrNeverPaid = errors.New("the payments do not cover the interest")

var (
	hundred = decimal.NewFromInt(100)
	twelve  = decimal.NewFromInt(12)
)

// Closing is when one debt ends.
type Closing struct {
	DebtID int64
	Name   string
	Month  time.Time // the first day of the month of the last payment
}

// Schedule is one way of paying everything off.
type Schedule struct {
	Method   string
	Months   int
	Interest decimal.Decimal // paid on top of the balances
	Closings []Closing       // in order of closing
}

type planned struct {
	Debt
	left decimal.Decimal
}

// Schedules plans both ways. Every month interest is added at a twelfth of the
// annual rate, each debt gets its minimum, and the rest of the budget goes to
// the first debt in line. What a closed debt no longer takes stays in the
// budget, so the payment rolls over to the next one.
func Schedules(list []Debt, extra decimal.Decimal, start time.Time) (avalanche, snowball Schedule, err error) {
	if avalanche, err = schedule(list, extra, start, Avalanche); err != nil {
		return Schedule{}, Schedule{}, err
	}
	snowball, err = schedule(list, extra, start, Snowball)
	return avalanche, snowball, err
}

func schedule(list []Debt, extra decimal.Decimal, start time.Time, method string) (Schedule, error) {
	out := Schedule{Method: method, Interest: decimal.Zero, Closings: []Closing{}}
	queue := make([]*planned, 0, len(list))
	budget := extra
	for _, d := range list {
		if d.Balance.IsPositive() {
			queue = append(queue, &planned{Debt: d, left: d.Balance})
			budget = budget.Add(d.MinPayment)
		}
	}
	slices.SortStableFunc(queue, func(a, b *planned) int {
		if method == Snowball {
			return cmp.Or(a.left.Cmp(b.left), b.Rate.Cmp(a.Rate))
		}
		return cmp.Or(b.Rate.Cmp(a.Rate), a.left.Cmp(b.left))
	})
	first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	for month := 1; len(queue) > 0; month++ {
		if month > maxPlanMonths {
			return Schedule{}, ErrNeverPaid
		}
		before, after := decimal.Zero, decimal.Zero
		for _, d := range queue {
			before = before.Add(d.left)
			interest := d.left.Mul(d.Rate).Div(hundred).Div(twelve).Round(2)
			d.left = d.left.Add(interest)
			out.Interest = out.Interest.Add(interest)
		}
		left := budget
		for _, d := range queue {
			pay := decimal.Min(d.MinPayment, d.left, left)
			d.left = d.left.Sub(pay)
			left = left.Sub(pay)
		}
		for _, d := range queue {
			pay := decimal.Min(d.left, left)
			d.left = d.left.Sub(pay)
			left = left.Sub(pay)
			after = after.Add(d.left)
		}
		if !after.LessThan(before) {
			return Schedule{}, ErrNeverPaid
		}
		queue = slices.DeleteFunc(queue, func(d *planned) bool {
			if d.left.IsPositive() {
				return false
			}
			out.Closings = append(out.Closings, Closing{DebtID: d.ID, Name: d.Name, Month: first.AddDate(0, month, 0)})
			return true
		})
		out.Months = month
	}
	return out, nil
}
