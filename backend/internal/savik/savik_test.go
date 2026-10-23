package savik

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

var today = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

func month(expense, income int64) ledger.Summary {
	var items []ledger.CategoryTotal
	if expense > 0 {
		items = append(items, ledger.CategoryTotal{Category: ledger.CategoryFood, Sum: decimal.NewFromInt(expense), Count: 1})
	}
	if income > 0 {
		items = append(items, ledger.CategoryTotal{Category: ledger.CategorySalary, Sum: decimal.NewFromInt(income), Count: 1})
	}
	return ledger.NewSummary(items)
}

func TestOfWithoutPlan(t *testing.T) {
	for _, tt := range []struct {
		expense, income int64
		want            State
	}{
		{0, 0, Neutral},
		{5000, 0, Neutral}, // incomes are not recorded: no verdict
		{5000, 80000, Neutral},
		{80000, 80000, Neutral},
		{80001, 80000, Empty},
	} {
		if got := Of(Input{Today: today, Month: month(tt.expense, tt.income)}); got != tt.want {
			t.Errorf("expense %d, income %d: %s, want %s", tt.expense, tt.income, got, tt.want)
		}
	}
}

func TestOf(t *testing.T) {
	n := decimal.NewFromInt
	ready := func(available, limit, spentToday int64) *budget.Budget {
		return &budget.Budget{Ready: true, Available: n(available), Limit: n(limit), SpentToday: n(spentToday)}
	}
	goal := func(saved, target int64, achievedDaysAgo int) budget.Goal {
		g := budget.Goal{Saved: n(saved), Target: n(target)}
		if achievedDaysAgo >= 0 {
			d := today.AddDate(0, 0, -achievedDaysAgo)
			g.AchievedOn = &d
		}
		return g
	}
	for _, tt := range []struct {
		name string
		in   Input
		want State
	}{
		{"calm", Input{Budget: ready(30000, 2000, 500)}, Neutral},
		{"goal reached yesterday", Input{Budget: ready(0, 0, 0), Goals: []budget.Goal{goal(100, 100, 1)}}, Full},
		{"goal reached a week ago", Input{Budget: ready(30000, 2000, 0), Goals: []budget.Goal{goal(100, 100, 7)}}, Fed},
		{"nothing left until payday", Input{Budget: ready(1000, 1000, 1000)}, Empty},
		{"over today's limit", Input{Budget: ready(30000, 2000, 2500)}, Hungry},
		{"no income known yet", Input{Budget: &budget.Budget{SpentToday: n(500)}}, Neutral},
		{"no income in the plan, the month is negative", Input{Budget: &budget.Budget{}, Month: month(9000, 8000)}, Empty},
		{"the plan wins over the month", Input{Budget: ready(30000, 2000, 0), Month: month(9000, 8000)}, Neutral},
		{"80% saved", Input{Budget: ready(30000, 2000, 0), Goals: []budget.Goal{goal(10, 100, -1), goal(80, 100, -1)}}, Fed},
		{"hungry beats fed", Input{Budget: ready(30000, 2000, 3000), Goals: []budget.Goal{goal(90, 100, -1)}}, Hungry},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.Today = today
			if got := Of(tt.in); got != tt.want {
				t.Errorf("Of = %s, want %s", got, tt.want)
			}
		})
	}
}
