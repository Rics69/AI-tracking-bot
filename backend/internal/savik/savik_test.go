package savik

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

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

func TestOf(t *testing.T) {
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
		if got := Of(month(tt.expense, tt.income)); got != tt.want {
			t.Errorf("expense %d, income %d: %s, want %s", tt.expense, tt.income, got, tt.want)
		}
	}
}
