package postgres

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

func TestBudgetPlan(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewBudgetRepository(testPool)

	if _, err := r.Plan(ctx, 1); !errors.Is(err, budget.ErrNotFound) {
		t.Fatalf("no plan yet: %v", err)
	}
	income := amount("80000")
	p, err := r.SavePlan(ctx, budget.Plan{UserID: 1, Paydays: []int{5, 20}, ExpectedIncome: &income, ReminderHour: 9, Reminders: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Paydays, []int{5, 20}) || !p.ExpectedIncome.Equal(income) || p.GoalContribution != nil {
		t.Errorf("saved plan = %+v", p)
	}
	// Saving again replaces the plan.
	if p, err = r.SavePlan(ctx, budget.Plan{UserID: 1, Paydays: []int{10}, ReminderHour: 8}); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Plan(ctx, 1); !slices.Equal(got.Paydays, []int{10}) || got.ExpectedIncome != nil || got.Reminders {
		t.Errorf("replaced plan = %+v", got)
	}
	if _, err := r.SavePlan(ctx, budget.Plan{UserID: 404, Paydays: []int{1}}); !errors.Is(err, ledger.ErrUnknownUser) {
		t.Errorf("plan of an unknown user: %v", err)
	}
	if err := r.DeletePlan(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.DeletePlan(ctx, 1); !errors.Is(err, budget.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestGoalsAndDeposits(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewBudgetRepository(testPool)

	deadline := day(31)
	g, err := r.CreateGoal(ctx, budget.Goal{UserID: 1, Title: "Отпуск", Target: amount("1000"), Deadline: &deadline})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Saved.IsZero() || !g.Deadline.Equal(deadline) {
		t.Errorf("new goal = %+v", g)
	}
	if _, err := r.Goal(ctx, 2, g.ID); !errors.Is(err, budget.ErrNotFound) {
		t.Errorf("Boris sees Anna's goal: %v", err)
	}
	if _, err := r.AddDeposit(ctx, budget.Deposit{GoalID: g.ID, UserID: 2, Amount: amount("1"), Date: day(1)}, nil); !errors.Is(err, budget.ErrNotFound) {
		t.Errorf("Boris deposits to Anna's goal: %v", err)
	}

	achieved := day(20)
	if g, err = r.AddDeposit(ctx, budget.Deposit{GoalID: g.ID, UserID: 1, Amount: amount("700"), Date: day(10)}, nil); err != nil {
		t.Fatal(err)
	}
	if g, err = r.AddDeposit(ctx, budget.Deposit{GoalID: g.ID, UserID: 1, Amount: amount("400"), Date: day(20)}, &achieved); err != nil {
		t.Fatal(err)
	}
	if g, err = r.AddDeposit(ctx, budget.Deposit{GoalID: g.ID, UserID: 1, Amount: amount("-50"), Date: day(21)}, &achieved); err != nil {
		t.Fatal(err)
	}
	if !g.Saved.Equal(amount("1050")) || g.AchievedOn == nil || !g.AchievedOn.Equal(achieved) {
		t.Errorf("after deposits = %+v", g)
	}
	if saved, _ := r.Saved(ctx, 1, day(15), day(31)); !saved.Equal(amount("350")) {
		t.Errorf("saved since the 15th = %s, want 350", saved)
	}

	g.Title = "Море"
	if g, err = r.UpdateGoal(ctx, g); err != nil || g.Title != "Море" || !g.Saved.Equal(amount("1050")) {
		t.Errorf("updated goal = %+v, %v", g, err)
	}
	second, _ := r.CreateGoal(ctx, budget.Goal{UserID: 1, Title: "Ноутбук", Target: amount("90000")})
	if goals, _ := r.Goals(ctx, 1); len(goals) != 2 || goals[0].ID != g.ID || goals[1].ID != second.ID {
		t.Errorf("goals = %+v", goals)
	}
	if goals, _ := r.Goals(ctx, 2); len(goals) != 0 {
		t.Errorf("Boris's goals = %+v", goals)
	}

	// Deleting a goal frees its money.
	if err := r.DeleteGoal(ctx, 2, g.ID); !errors.Is(err, budget.ErrNotFound) {
		t.Errorf("Boris deletes Anna's goal: %v", err)
	}
	if err := r.DeleteGoal(ctx, 1, g.ID); err != nil {
		t.Fatal(err)
	}
	if saved, _ := r.Saved(ctx, 1, day(1), day(31)); !saved.IsZero() {
		t.Errorf("deposits of a deleted goal still count: %s", saved)
	}
}

func TestRegularPayments(t *testing.T) {
	ops, _ := setup(t)
	ctx := context.Background()
	r := NewBudgetRepository(testPool)

	rent, err := r.CreatePayment(ctx, budget.RegularPayment{UserID: 1, Title: "Аренда", Amount: amount("45000"), Day: 25, Category: ledger.CategoryHousing})
	if err != nil {
		t.Fatal(err)
	}
	phone, _ := r.CreatePayment(ctx, budget.RegularPayment{UserID: 1, Title: "Связь", Amount: amount("600"), Day: 1, Category: ledger.CategoryOther})
	if list, _ := r.Payments(ctx, 1); len(list) != 2 || list[0].ID != phone.ID || list[1].LastPaidOn != nil {
		t.Errorf("payments by day = %+v", list)
	}

	paid, err := ops.Create(ctx, ledger.Operation{UserID: 1, Amount: amount("45000"), Category: ledger.CategoryHousing,
		Date: day(24), RegularPaymentID: &rent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if paid.RegularPaymentID == nil || *paid.RegularPaymentID != rent.ID {
		t.Errorf("link lost: %+v", paid)
	}
	if got, _ := r.Payment(ctx, 1, rent.ID); got.LastPaidOn == nil || !got.LastPaidOn.Equal(day(24)) {
		t.Errorf("last paid = %v", got.LastPaidOn)
	}

	// Boris cannot pay Anna's rent, and does not see it.
	var verr *ledger.ValidationError
	_, err = ops.Create(ctx, ledger.Operation{UserID: 2, Amount: amount("1"), Category: ledger.CategoryHousing, Date: day(24), RegularPaymentID: &rent.ID})
	if !errors.As(err, &verr) || verr.Field != "regular_payment_id" {
		t.Errorf("Boris links Anna's payment: %v", err)
	}
	if _, err := r.Payment(ctx, 2, rent.ID); !errors.Is(err, budget.ErrNotFound) {
		t.Errorf("Boris sees Anna's payment: %v", err)
	}

	rent.Amount = amount("47000")
	if rent, err = r.UpdatePayment(ctx, rent); err != nil || !rent.Amount.Equal(amount("47000")) {
		t.Errorf("updated payment = %+v, %v", rent, err)
	}
	if ids, _ := r.Reminded(ctx); !slices.Equal(ids, []int64{1}) {
		t.Errorf("reminded = %v, want Anna with payments", ids)
	}

	// Deleting the payment keeps the expense, unlinked.
	if err := r.DeletePayment(ctx, 1, rent.ID); err != nil {
		t.Fatal(err)
	}
	if e, err := ops.Get(ctx, ledger.OwnedBy(1), paid.ID); err != nil || e.RegularPaymentID != nil {
		t.Errorf("expense after the payment was deleted = %+v, %v", e, err)
	}
}

func TestRemindedFollowsPlans(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewBudgetRepository(testPool)
	if _, err := r.SavePlan(ctx, budget.Plan{UserID: 1, Paydays: []int{5}, Reminders: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SavePlan(ctx, budget.Plan{UserID: 2, Paydays: []int{5}, Reminders: false}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := r.Reminded(ctx); !slices.Equal(ids, []int64{1}) {
		t.Errorf("reminded = %v, want only Anna", ids)
	}
}

func TestNotificationRunsMigrationKeepsWeeklyRuns(t *testing.T) {
	setup(t)
	ctx := context.Background()
	db := stdlib.OpenDBFromPool(testPool)
	defer func() { _ = db.Close() }()
	dir, _ := fs.Sub(migrations, "migrations")
	provider, err := goose.NewProvider(goose.DialectPostgres, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := provider.Up(ctx); err != nil {
			t.Error(err)
		}
	}()
	if _, err := testPool.Exec(ctx, `INSERT INTO report_runs (week_start, user_id) VALUES ('2026-10-12', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	runs := NewNotificationRunRepository(testPool)
	if ok, err := runs.Claim(ctx, "weekly", day(12).Format(time.DateOnly), 1); ok || err != nil {
		t.Errorf("a report sent before the migration was claimed again: %v %v", ok, err)
	}
}
