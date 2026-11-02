package debts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
)

const anna int64 = 1

var (
	ctx   = context.Background()
	today = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	now   = today.Add(12 * time.Hour)
)

func n(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func debt(id int64, name, balance, rate, minimum string) debts.Debt {
	return debts.Debt{ID: id, Name: name, Balance: n(balance), Rate: n(rate), MinPayment: n(minimum)}
}

// The reference table comes from a separate model in another language, with
// its own decimal arithmetic, not from this code.
func TestScheduleMatchesTheReferenceToTheKopeck(t *testing.T) {
	first := []debts.Debt{
		debt(1, "Карта", "150000", "29.9", "4500"),
		debt(2, "Кредит", "300000", "17.5", "9000"),
		debt(3, "Рассрочка", "40000", "0", "5000"),
	}
	second := []debts.Debt{
		debt(1, "МФО", "20000", "292", "3000"),
		debt(2, "Карта", "100000", "39.9", "3000"),
		debt(3, "Друг", "5000", "0", "0"),
	}
	tests := []struct {
		name     string
		list     []debts.Debt
		extra    string
		av, sn   int
		avI, snI string
		avC, snC map[string]int // months to close
	}{
		{"cards and a loan", first, "10000", 21, 21, "87886.97", "91468.02",
			map[string]int{"Рассрочка": 8, "Карта": 12, "Кредит": 21}, map[string]int{"Рассрочка": 3, "Карта": 12, "Кредит": 21}},
		{"a payday loan", second, "5000", 16, 18, "50816.27", "65472.20",
			map[string]int{"МФО": 5, "Карта": 16, "Друг": 16}, map[string]int{"Друг": 1, "МФО": 7, "Карта": 18}},
	}
	month := func(c debts.Closing) int {
		start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		return (c.Month.Year()-start.Year())*12 + int(c.Month.Month()) - int(start.Month())
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			av, sn, err := debts.Schedules(tt.list, n(tt.extra), today)
			if err != nil {
				t.Fatal(err)
			}
			if av.Months != tt.av || !av.Interest.Equal(n(tt.avI)) {
				t.Errorf("avalanche: %d months, %s interest; want %d, %s", av.Months, av.Interest, tt.av, tt.avI)
			}
			if sn.Months != tt.sn || !sn.Interest.Equal(n(tt.snI)) {
				t.Errorf("snowball: %d months, %s interest; want %d, %s", sn.Months, sn.Interest, tt.sn, tt.snI)
			}
			if !av.Interest.LessThanOrEqual(sn.Interest) {
				t.Errorf("the avalanche must not cost more: %s > %s", av.Interest, sn.Interest)
			}
			for _, c := range av.Closings {
				if month(c) != tt.avC[c.Name] {
					t.Errorf("avalanche closes %s in %d, want %d", c.Name, month(c), tt.avC[c.Name])
				}
			}
			for _, c := range sn.Closings {
				if month(c) != tt.snC[c.Name] {
					t.Errorf("snowball closes %s in %d, want %d", c.Name, month(c), tt.snC[c.Name])
				}
			}
		})
	}
}

func TestAvalancheNeverCostsMore(t *testing.T) {
	for _, extra := range []string{"0", "1000", "5000", "50000"} {
		list := []debts.Debt{
			debt(1, "a", "10000", "60", "1000"),
			debt(2, "b", "90000", "10", "3000"),
			debt(3, "c", "45000", "25", "2000"),
		}
		av, sn, err := debts.Schedules(list, n(extra), today)
		if err != nil {
			t.Fatal(err)
		}
		if av.Interest.GreaterThan(sn.Interest) {
			t.Errorf("extra %s: avalanche %s > snowball %s", extra, av.Interest, sn.Interest)
		}
	}
}

func TestPaymentsThatDoNotCoverInterest(t *testing.T) {
	_, _, err := debts.Schedules([]debts.Debt{debt(1, "МФО", "100000", "292", "1000")}, n("0"), today)
	if !errors.Is(err, debts.ErrNeverPaid) {
		t.Errorf("err = %v", err)
	}
}

func TestNoDebtsNoPlan(t *testing.T) {
	av, _, err := debts.Schedules(nil, n("1000"), today)
	if err != nil || av.Months != 0 || !av.Interest.IsZero() {
		t.Errorf("plan = %+v, %v", av, err)
	}
}

type env struct {
	s     *debts.Service
	ops   *ledger.Service
	repo  *memory.OperationRepository
	money *accounts.Service
}

func setup() env {
	repo := memory.NewOperationRepository()
	l := ledger.NewService(repo, time.UTC)
	rates := memory.NewRates()
	money := accounts.NewService(memory.NewAccounts(repo), rates)
	return env{s: debts.NewService(memory.NewDebts(), l, money), ops: l, repo: repo, money: money}
}

func TestPaymentIsAnExpenseAndClosesTheDebt(t *testing.T) {
	e := setup()
	d, err := e.s.Create(ctx, debts.Debt{UserID: anna, Name: "Кредитка", Kind: debts.KindCard, Principal: n("10000"), Rate: n("30")}, today)
	if err != nil {
		t.Fatal(err)
	}
	in := debts.PaymentInput{UserID: anna, DebtID: d.ID, Amount: n("4000"), Today: today, Now: now}
	if d, err = e.s.Pay(ctx, in); err != nil || !d.Balance.Equal(n("6000")) || d.ClosedAt != nil {
		t.Fatalf("after a payment = %+v, %v", d, err)
	}
	ops := e.repo.All()
	if len(ops) != 1 || !ops[0].Amount.Equal(n("4000")) || ops[0].Kind() != ledger.KindExpense {
		t.Fatalf("the money must leave as an expense: %+v", ops)
	}

	in.Amount = n("7000")
	if _, err := e.s.Pay(ctx, in); err == nil {
		t.Error("paid more than is left")
	}
	in.Amount = n("6000")
	if d, err = e.s.Pay(ctx, in); err != nil || !d.Balance.IsZero() || d.ClosedAt == nil {
		t.Fatalf("closing = %+v, %v", d, err)
	}
	if list, total, _ := e.s.List(ctx, anna); len(list) != 0 || !total.IsZero() {
		t.Errorf("a closed debt is still listed: %+v", list)
	}

	// A payment taken back reopens the debt and removes its expense.
	payments, _ := e.s.Payments(ctx, anna, d.ID)
	if err := e.s.CancelPayment(ctx, anna, payments[0].ID); err != nil {
		t.Fatal(err)
	}
	if list, total, _ := e.s.List(ctx, anna); len(list) != 1 || !total.Equal(n("6000")) {
		t.Errorf("after cancelling = %+v, %s", list, total)
	}
	if ops := e.repo.All(); len(ops) != 1 {
		t.Errorf("operations = %+v", ops)
	}
}

func TestSomeoneElsesDebt(t *testing.T) {
	e := setup()
	d, _ := e.s.Create(ctx, debts.Debt{UserID: anna, Name: "Кредит", Principal: n("1000")}, today)
	if _, err := e.s.Pay(ctx, debts.PaymentInput{UserID: 2, DebtID: d.ID, Amount: n("1"), Today: today, Now: now}); !errors.Is(err, debts.ErrNotFound) {
		t.Errorf("pay = %v", err)
	}
	if err := e.s.Delete(ctx, 2, d.ID); !errors.Is(err, debts.ErrNotFound) {
		t.Errorf("delete = %v", err)
	}
}

func TestCorrectingTheBalance(t *testing.T) {
	e := setup()
	d, _ := e.s.Create(ctx, debts.Debt{UserID: anna, Name: "Кредит", Principal: n("1000")}, today)
	balance := n("1250")
	got, err := e.s.Update(ctx, anna, d.ID, debts.Patch{Balance: &balance}, now)
	if err != nil || !got.Balance.Equal(balance) {
		t.Fatalf("corrected = %+v, %v", got, err)
	}
	if len(e.repo.All()) != 0 {
		t.Error("a correction is not an expense")
	}
}

func TestNetWorthAndHistory(t *testing.T) {
	e := setup()
	if _, err := e.ops.Create(ctx, ledger.CreateInput{UserID: anna, Amount: n("80000"), Category: ledger.CategorySalary, Today: today}); err != nil {
		t.Fatal(err)
	}
	d, _ := e.s.Create(ctx, debts.Debt{UserID: anna, Name: "Кредит", Principal: n("30000")}, today)
	got, err := e.s.NetWorth(ctx, anna, today)
	if err != nil || !got.Net().Equal(n("50000")) {
		t.Fatalf("net worth = %+v, %v", got, err)
	}
	// A payment moves both sides: the net worth stays.
	if _, err := e.s.Pay(ctx, debts.PaymentInput{UserID: anna, DebtID: d.ID, Amount: n("10000"), Today: today, Now: now}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.s.NetWorth(ctx, anna, today)
	if !got.Net().Equal(n("50000")) || !got.Assets.Equal(n("70000")) || !got.Debts.Equal(n("20000")) {
		t.Errorf("after a payment = %+v", got)
	}
	if err := e.s.Snap(ctx, anna, today); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Snap(ctx, anna, today.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Snap(ctx, anna, today.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.s.History(ctx, anna, today.AddDate(0, 1, 0)); len(list) != 2 {
		t.Errorf("one snapshot a month: %+v", list)
	}
}

func TestDueOn(t *testing.T) {
	e := setup()
	day := 31
	_, _ = e.s.Create(ctx, debts.Debt{UserID: anna, Name: "Кредит", Principal: n("1000"), DueDay: &day}, today)
	last := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	if got, _ := e.s.DueOn(ctx, anna, last); len(got) != 1 {
		t.Errorf("the 31st in a short month is its last day: %+v", got)
	}
	if got, _ := e.s.DueOn(ctx, anna, last.AddDate(0, 0, -1)); len(got) != 0 {
		t.Errorf("another day: %+v", got)
	}
}

func TestByName(t *testing.T) {
	e := setup()
	_, _ = e.s.Create(ctx, debts.Debt{UserID: anna, Name: "Кредитка Альфа", Principal: n("1000")}, today)
	_, _ = e.s.Create(ctx, debts.Debt{UserID: anna, Name: "Ипотека", Principal: n("1000")}, today)
	if d, err := e.s.ByName(ctx, anna, "кредитка"); err != nil || d.Name != "Кредитка Альфа" {
		t.Errorf("found = %+v, %v", d, err)
	}
	if _, err := e.s.ByName(ctx, anna, "авто"); !errors.Is(err, debts.ErrNotFound) {
		t.Errorf("nothing = %v", err)
	}
}
