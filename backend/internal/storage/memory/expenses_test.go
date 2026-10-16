package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
)

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func add(t *testing.T, r *ExpenseRepository, user int64, sum int64, c expense.Category, d int, comment string) expense.Expense {
	t.Helper()
	e, err := r.Create(context.Background(), expense.Expense{
		UserID: user, Amount: decimal.NewFromInt(sum), Category: c, SpentAt: day(d), Comment: comment,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestListMatchesPostgresSemantics(t *testing.T) {
	r := NewExpenseRepository()
	ctx := context.Background()
	taxi := add(t, r, 1, 1200, expense.CategoryTransport, 15, "Такси")
	coffee := add(t, r, 1, 350, expense.CategoryFood, 16, "кофе")
	old := add(t, r, 1, 100, expense.CategoryFood, 1, "обед")
	add(t, r, 2, 999, expense.CategoryFood, 16, "чужое")

	page, _ := r.List(ctx, expense.OwnedBy(1), expense.Filter{Limit: 10})
	if page.Total != 3 || page.Items[0].ID != coffee.ID || page.Items[2].ID != old.ID ||
		!page.Sum.Equal(decimal.NewFromInt(1650)) {
		t.Errorf("by spent date: %+v", page)
	}

	page, _ = r.List(ctx, expense.OwnedBy(1), expense.Filter{Sort: expense.SortByCreatedAt, Limit: 1})
	if page.Items[0].ID != old.ID {
		t.Errorf("last added = %d, want %d", page.Items[0].ID, old.ID)
	}

	page, _ = r.List(ctx, expense.OwnedBy(1), expense.Filter{Query: "такси", From: new(day(10)), Limit: 10})
	if page.Total != 1 || page.Items[0].ID != taxi.ID {
		t.Errorf("query+period: %+v", page.Items)
	}

	sum, _ := r.Summary(ctx, expense.OwnedBy(1), expense.Filter{})
	if len(sum.Items) != 2 || sum.Items[0].Category != expense.CategoryTransport || sum.Count != 3 {
		t.Errorf("summary: %+v", sum)
	}

	if err := r.Delete(ctx, expense.OwnedBy(2), coffee.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Errorf("foreign delete: %v", err)
	}
	if page, _ := r.List(ctx, expense.Scope{}, expense.Filter{Limit: 10}); page.Total != 0 {
		t.Errorf("zero scope sees %d", page.Total)
	}
}
