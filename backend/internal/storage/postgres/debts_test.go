package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

func TestDebts(t *testing.T) {
	ops, _ := setup(t)
	ctx := context.Background()
	repo := NewDebtRepository(testPool)
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	now := today.Add(time.Hour)
	l := ledger.NewService(ops, time.UTC)
	s := debts.NewService(repo, l, nil)
	money := func(v string) decimal.Decimal { return decimal.RequireFromString(v) }

	day := 15
	d, err := s.Create(ctx, debts.Debt{UserID: 1, Name: "Кредитка", Kind: debts.KindCard, Principal: money("10000"),
		Rate: money("29.9"), MinPayment: money("500"), DueDay: &day}, today)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Balance.Equal(money("10000")) || !d.Rate.Equal(money("29.9")) || *d.DueDay != 15 {
		t.Fatalf("created = %+v", d)
	}
	if _, err := repo.Get(ctx, 2, d.ID); !errors.Is(err, debts.ErrNotFound) {
		t.Errorf("someone else's debt: %v", err)
	}

	d, err = s.Pay(ctx, debts.PaymentInput{UserID: 1, DebtID: d.ID, Amount: money("4000"), Today: today, Now: now})
	if err != nil || !d.Balance.Equal(money("6000")) {
		t.Fatalf("paid = %+v, %v", d, err)
	}

	// The payment and its expense are one fact: without the expense the money
	// has not left, so the debt is what it was.
	payments, err := s.Payments(ctx, 1, d.ID)
	if err != nil || len(payments) != 1 || payments[0].OperationID == nil {
		t.Fatalf("payments = %+v, %v", payments, err)
	}
	if err := l.Delete(ctx, ledger.OwnedBy(1), *payments[0].OperationID); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, 1, d.ID); !got.Balance.Equal(money("10000")) {
		t.Errorf("after deleting the expense = %s", got.Balance)
	}

	// Closing and deleting the last expense: the debt is open again.
	if d, err = s.Pay(ctx, debts.PaymentInput{UserID: 1, DebtID: d.ID, Amount: money("10000"), Today: today, Now: now}); err != nil || d.ClosedAt == nil {
		t.Fatalf("closed = %+v, %v", d, err)
	}
	if open, _ := repo.Open(ctx); len(open) != 0 {
		t.Errorf("open = %+v", open)
	}
	if ids, _ := repo.ClosedUsers(ctx); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("closed users = %v", ids)
	}
	payments, _ = s.Payments(ctx, 1, d.ID)
	if err := l.Delete(ctx, ledger.OwnedBy(1), *payments[0].OperationID); err != nil {
		t.Fatal(err)
	}
	if list, total, _ := s.List(ctx, 1); len(list) != 1 || !total.Equal(money("10000")) {
		t.Errorf("reopened = %+v, %s", list, total)
	}

	// A taken back payment removes its expense.
	if _, err = s.Pay(ctx, debts.PaymentInput{UserID: 1, DebtID: d.ID, Amount: money("1000"), Today: today, Now: now}); err != nil {
		t.Fatal(err)
	}
	payments, _ = s.Payments(ctx, 1, d.ID)
	if err := s.CancelPayment(ctx, 2, payments[0].ID); !errors.Is(err, debts.ErrNotFound) {
		t.Errorf("someone else cancelled: %v", err)
	}
	if err := s.CancelPayment(ctx, 1, payments[0].ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM operations WHERE user_id = 1`).Scan(&left); err != nil || left != 0 {
		t.Errorf("operations left = %d, %v", left, err)
	}

	// One row a month, the last value stays.
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, v := range []string{"100", "200"} {
		if err := repo.SaveSnapshot(ctx, 1, debts.Snapshot{Month: month, Assets: money(v), Debts: money("50")}); err != nil {
			t.Fatal(err)
		}
	}
	shots, err := repo.Snapshots(ctx, 1, month)
	if err != nil || len(shots) != 1 || !shots[0].Net().Equal(money("150")) {
		t.Errorf("snapshots = %+v, %v", shots, err)
	}

	if err := s.Delete(ctx, 1, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, 1, d.ID); !errors.Is(err, debts.ErrNotFound) {
		t.Errorf("deleted twice: %v", err)
	}
}
