package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func add(t *testing.T, r *OperationRepository, user int64, sum int64, c ledger.Category, d int, comment string) ledger.Operation {
	t.Helper()
	e, err := r.Create(context.Background(), ledger.Operation{
		UserID: user, Amount: decimal.NewFromInt(sum), Category: c, Date: day(d), Comment: comment,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestListMatchesPostgresSemantics(t *testing.T) {
	r := NewOperationRepository()
	ctx := context.Background()
	taxi := add(t, r, 1, 1200, ledger.CategoryTransport, 15, "Такси")
	coffee := add(t, r, 1, 350, ledger.CategoryFood, 16, "кофе")
	old := add(t, r, 1, 100, ledger.CategoryFood, 1, "обед")
	add(t, r, 2, 999, ledger.CategoryFood, 16, "чужое")
	salary := add(t, r, 1, 80000, ledger.CategorySalary, 5, "зарплата")

	page, _ := r.List(ctx, ledger.OwnedBy(1), ledger.Filter{Limit: 10})
	if page.Total != 4 || page.Items[0].ID != coffee.ID || page.Items[3].ID != old.ID ||
		!page.ExpenseSum.Equal(decimal.NewFromInt(1650)) || !page.IncomeSum.Equal(decimal.NewFromInt(80000)) {
		t.Errorf("by date: %+v", page)
	}
	page, _ = r.List(ctx, ledger.OwnedBy(1), ledger.Filter{Kind: new(ledger.KindIncome), Limit: 10})
	if page.Total != 1 || page.Items[0].ID != salary.ID || !page.ExpenseSum.IsZero() {
		t.Errorf("incomes only: %+v", page)
	}

	page, _ = r.List(ctx, ledger.OwnedBy(1), ledger.Filter{Sort: ledger.SortByCreatedAt, Limit: 1})
	if page.Items[0].ID != salary.ID {
		t.Errorf("last added = %d, want %d", page.Items[0].ID, salary.ID)
	}

	page, _ = r.List(ctx, ledger.OwnedBy(1), ledger.Filter{Query: "такси", From: new(day(10)), Limit: 10})
	if page.Total != 1 || page.Items[0].ID != taxi.ID {
		t.Errorf("query+period: %+v", page.Items)
	}

	sum, _ := r.Summary(ctx, ledger.OwnedBy(1), ledger.Filter{})
	if len(sum.Items) != 3 || sum.Expense.Count != 3 || sum.Income.Count != 1 ||
		!sum.Balance().Equal(decimal.NewFromInt(78350)) {
		t.Errorf("summary: %+v", sum)
	}
	if spent := sum.Of(ledger.KindExpense); len(spent) != 2 || spent[0].Category != ledger.CategoryTransport {
		t.Errorf("expense categories: %+v", spent)
	}

	if err := r.Delete(ctx, ledger.OwnedBy(2), coffee.ID); !errors.Is(err, ledger.ErrNotFound) {
		t.Errorf("foreign delete: %v", err)
	}
	if page, _ := r.List(ctx, ledger.Scope{}, ledger.Filter{Limit: 10}); page.Total != 0 {
		t.Errorf("zero scope sees %d", page.Total)
	}
}
