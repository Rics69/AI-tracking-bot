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

	page, _ := r.List(ctx, ledger.OwnedBy(1), ledger.Filter{Limit: 10})
	if page.Total != 3 || page.Items[0].ID != coffee.ID || page.Items[2].ID != old.ID ||
		!page.Sum.Equal(decimal.NewFromInt(1650)) {
		t.Errorf("by spent date: %+v", page)
	}

	page, _ = r.List(ctx, ledger.OwnedBy(1), ledger.Filter{Sort: ledger.SortByCreatedAt, Limit: 1})
	if page.Items[0].ID != old.ID {
		t.Errorf("last added = %d, want %d", page.Items[0].ID, old.ID)
	}

	page, _ = r.List(ctx, ledger.OwnedBy(1), ledger.Filter{Query: "такси", From: new(day(10)), Limit: 10})
	if page.Total != 1 || page.Items[0].ID != taxi.ID {
		t.Errorf("query+period: %+v", page.Items)
	}

	sum, _ := r.Summary(ctx, ledger.OwnedBy(1), ledger.Filter{})
	if len(sum.Items) != 2 || sum.Items[0].Category != ledger.CategoryTransport || sum.Count != 3 {
		t.Errorf("summary: %+v", sum)
	}

	if err := r.Delete(ctx, ledger.OwnedBy(2), coffee.ID); !errors.Is(err, ledger.ErrNotFound) {
		t.Errorf("foreign delete: %v", err)
	}
	if page, _ := r.List(ctx, ledger.Scope{}, ledger.Filter{Limit: 10}); page.Total != 0 {
		t.Errorf("zero scope sees %d", page.Total)
	}
}
