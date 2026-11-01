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
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/flow"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func TestBudgetPlan(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewBudgetRepository(testPool)

	if _, err := r.Plan(ctx, 1); !errors.Is(err, budget.ErrNotFound) {
		t.Fatalf("no plan yet: %v", err)
	}
	income := amount("80000")
	p, err := r.SavePlan(ctx, budget.Plan{UserID: 1, Paydays: []int{5, 20}, ExpectedIncome: &income})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Paydays, []int{5, 20}) || !p.ExpectedIncome.Equal(income) || p.GoalContribution != nil {
		t.Errorf("saved plan = %+v", p)
	}
	// Saving again replaces the plan.
	if p, err = r.SavePlan(ctx, budget.Plan{UserID: 1, Paydays: []int{10}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Plan(ctx, 1); !slices.Equal(got.Paydays, []int{10}) || got.ExpectedIncome != nil {
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

func TestRemindedIsWhoHasSomethingToHear(t *testing.T) {
	_, users := setup(t)
	ctx := context.Background()
	r := NewBudgetRepository(testPool)
	if ids, _ := r.Reminded(ctx); len(ids) != 0 {
		t.Errorf("nothing set up: reminded = %v", ids)
	}
	if _, err := r.SavePlan(ctx, budget.Plan{UserID: 1, Paydays: []int{5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateGoal(ctx, budget.Goal{UserID: 2, Title: "Отпуск", Target: amount("1000")}); err != nil {
		t.Fatal(err)
	}
	ids, _ := r.Reminded(ctx)
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{1, 2}) {
		t.Errorf("reminded = %v", ids)
	}
	all, err := r.AllGoals(ctx)
	if err != nil || len(all) != 1 || all[0].UserID != 2 {
		t.Errorf("all goals = %+v, %v", all, err)
	}

	u, err := users.SetNotifications(ctx, 1, user.Notifications{ReminderHour: 8, Reminders: true, GoalUpdates: true})
	if err != nil || u.ReminderHour != 8 || u.ShowRates || !u.GoalUpdates {
		t.Errorf("notifications = %+v, %v", u.Notifications, err)
	}
	if u, _ = users.Get(ctx, 2); u.ReminderHour != user.DefaultReminderHour || !u.Reminders || !u.ShowRates || !u.GoalUpdates {
		t.Errorf("defaults = %+v", u.Notifications)
	}
}

func TestFlows(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewFlowRepository(testPool)
	if _, err := r.Get(ctx, 1); !errors.Is(err, flow.ErrNone) {
		t.Fatalf("no flow yet: %v", err)
	}
	s := flow.State{UserID: 1, Flow: "dream", Step: "dprice", Data: map[string]string{"title": "Ноутбук"}}
	if err := r.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	s.Step, s.Data["price"] = "dterm", "120000.5"
	if err := r.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, 1)
	if err != nil || got.Step != "dterm" || got.Data["title"] != "Ноутбук" || got.Data["price"] != "120000.5" || got.Expired(time.Now()) {
		t.Fatalf("flow = %+v, %v", got, err)
	}
	if n, err := r.DeleteOlderThan(ctx, time.Hour); err != nil || n != 0 {
		t.Errorf("a fresh flow was cleaned up: %d, %v", n, err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE user_flows SET updated_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	if n, err := r.DeleteOlderThan(ctx, flow.TTL); err != nil || n != 1 {
		t.Errorf("an old flow stayed: %d, %v", n, err)
	}
	if err := r.Delete(ctx, 1); err != nil {
		t.Errorf("delete of nothing: %v", err)
	}
}

func TestGameStore(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewGameRepository(testPool)
	if ok, err := r.AddXP(ctx, 1, 10, "quest", "a:1"); err != nil || !ok {
		t.Fatalf("xp = %v, %v", ok, err)
	}
	if ok, err := r.AddXP(ctx, 1, 10, "quest", "a:1"); err != nil || ok {
		t.Errorf("the same experience twice: %v, %v", ok, err)
	}
	if _, err := r.AddXP(ctx, 1, 5, "quest", "b:1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RaiseLevel(ctx, 1, 3, true); err != nil {
		t.Fatal(err)
	}
	if err := r.RaiseLevel(ctx, 1, 1, false); err != nil {
		t.Fatal(err)
	}
	if p, err := r.Profile(ctx, 1); err != nil || p.XP != 15 || p.Level != 3 || !p.LevelTested {
		t.Errorf("profile = %+v, %v", p, err)
	}

	for i, want := range []struct {
		streak  int
		advance bool
	}{{1, true}, {2, true}, {2, false}} {
		p, advanced, err := r.TouchStreak(ctx, 1, day(10+min(i, 1)))
		if err != nil || p.Streak != want.streak || advanced != want.advance {
			t.Errorf("touch %d = %+v, %v, %v", i, p, advanced, err)
		}
	}
	if p, _, _ := r.TouchStreak(ctx, 1, day(20)); p.Streak != 1 || p.BestStreak != 2 {
		t.Errorf("after a gap = %+v", p)
	}

	code, err := r.SetReferralCode(ctx, 1, "abc")
	if err != nil || code != "abc" {
		t.Fatalf("code = %q, %v", code, err)
	}
	if code, _ = r.SetReferralCode(ctx, 1, "other"); code != "abc" {
		t.Errorf("the code changed to %q", code)
	}
	if id, err := r.ReferrerOf(ctx, "abc"); err != nil || id != 1 {
		t.Errorf("referrer = %d, %v", id, err)
	}
	if err := r.AddReferral(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.AddReferral(ctx, 2, 2); err != nil {
		t.Fatal(err)
	}
	if id, _ := r.Referrer(ctx, 2); id != 1 {
		t.Errorf("the first referrer stays: %d", id)
	}

	if err := r.OfferQuest(ctx, 1, "a", "2026-10-24"); err != nil {
		t.Fatal(err)
	}
	if err := r.OfferQuest(ctx, 1, "a", "2026-10-24"); err != nil {
		t.Fatal(err)
	}
	open, err := r.OpenQuests(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("open = %+v, %v", open, err)
	}
	if ok, err := r.CloseQuest(ctx, 1, "a", "2026-10-24", game.StatusDone, time.Now()); err != nil || !ok {
		t.Errorf("close = %v, %v", ok, err)
	}
	if ok, _ := r.CloseQuest(ctx, 1, "a", "2026-10-24", game.StatusMissed, time.Now()); ok {
		t.Error("a closed quest closed again")
	}
	if rows, _ := r.UserQuests(ctx, 1); len(rows) != 1 || rows[0].Status != game.StatusDone || rows[0].DoneAt == nil {
		t.Errorf("rows = %+v", rows)
	}

	if ok, err := r.Join(ctx, "c", 1, day(10)); err != nil || !ok {
		t.Fatalf("join = %v, %v", ok, err)
	}
	if ok, _ := r.Join(ctx, "c", 1, day(11)); ok {
		t.Error("joined twice")
	}
	if _, err := r.Join(ctx, "c", 2, day(10)); err != nil {
		t.Fatal(err)
	}
	mark := game.DayMark{Slug: "c", UserID: 1, DayNo: 1, Date: day(10), Status: game.DayLater}
	if ok, err := r.MarkDay(ctx, mark); err != nil || !ok {
		t.Fatalf("mark = %v, %v", ok, err)
	}
	if later, _ := r.LaterMarks(ctx, day(10)); len(later) != 1 {
		t.Errorf("later = %+v", later)
	}
	mark.Status = game.DayDone
	if ok, _ := r.MarkDay(ctx, mark); !ok {
		t.Error("a postponed day was not done")
	}
	mark.Status = game.DaySkipped
	if ok, _ := r.MarkDay(ctx, mark); ok {
		t.Error("a done day changed")
	}
	if done, total, err := r.DayShare(ctx, "c", day(10)); err != nil || done != 1 || total != 2 {
		t.Errorf("share = %d of %d, %v", done, total, err)
	}
	parts, err := r.Participations(ctx, 1)
	if err != nil || len(parts) != 1 || parts[0].DoneDays != 1 {
		t.Fatalf("parts = %+v, %v", parts, err)
	}
	if err := r.SetParticipant(ctx, "c", 1, game.ParticipantFinished); err != nil {
		t.Fatal(err)
	}
	if active, _ := r.Participants(ctx); len(active) != 1 || active[0].UserID != 2 {
		t.Errorf("active = %+v", active)
	}
	st, err := r.ChallengeStats(ctx, "c")
	if err != nil || st.Participants != 2 || st.Finished != 1 || st.DoneByDay[1] != 1 {
		t.Errorf("stats = %+v, %v", st, err)
	}

	if err := r.AddReview(ctx, 1, map[string]string{"question": "С чего начать?"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reviews, err := r.Reviews(ctx)
	if err != nil || len(reviews) != 1 || reviews[0].Status != "new" || reviews[0].Answers["question"] != "С чего начать?" {
		t.Fatalf("reviews = %+v, %v", reviews, err)
	}
	if err := r.MarkReviewsSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if reviews, _ = r.Reviews(ctx); reviews[0].Status != "seen" {
		t.Errorf("after seeing = %+v", reviews)
	}
}

func TestActiveSince(t *testing.T) {
	ops, _ := setup(t)
	ctx := context.Background()
	mustCreate(t, ops, 1, "350", ledger.CategoryFood, 20, "кофе")
	active, err := NewGameRepository(testPool).ActiveSince(ctx, time.Now().Add(-time.Hour))
	if err != nil || len(active) != 1 || active[1].IsZero() {
		t.Errorf("active = %+v, %v", active, err)
	}
}

func TestRates(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewRateRepository(testPool)
	d := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }
	if err := r.Save(ctx, []rates.Rate{
		{Code: "USD", Date: d(30), Value: amount("80.1")},
		{Code: "EUR", Date: d(30), Value: amount("90.5")},
		{Code: "USD", Date: d(31), Value: amount("80.2")},
	}); err != nil {
		t.Fatal(err)
	}
	// The same day again: the Bank of Russia corrected it.
	if err := r.Save(ctx, []rates.Rate{{Code: "USD", Date: d(31), Value: amount("80.3")}}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Latest(ctx, []string{"USD", "EUR"})
	if err != nil || len(got) != 2 {
		t.Fatalf("latest = %+v, %v", got, err)
	}
	if got[0].Code != "USD" || !got[0].Date.Equal(d(31)) || !got[0].Value.Equal(amount("80.3")) || !got[1].Date.Equal(d(30)) {
		t.Errorf("latest = %+v", got)
	}
}

func TestRemindersMoveFromThePlanToTheUser(t *testing.T) {
	_, users := setup(t)
	ctx := context.Background()
	db := stdlib.OpenDBFromPool(testPool)
	defer func() { _ = db.Close() }()
	dir, _ := fs.Sub(migrations, "migrations")
	provider, err := goose.NewProvider(goose.DialectPostgres, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := provider.Up(ctx); err != nil {
			t.Error(err)
		}
	}()
	if _, err := testPool.Exec(ctx, `INSERT INTO budget_plans (user_id, paydays, reminder_hour, reminders)
		VALUES (1, '{5}', 7, false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if u, _ := users.Get(ctx, 1); u.ReminderHour != 7 || u.Reminders || !u.ShowRates || !u.GoalUpdates {
		t.Errorf("Anna = %+v", u.Notifications)
	}
	if u, _ := users.Get(ctx, 2); u.ReminderHour != user.DefaultReminderHour || !u.Reminders {
		t.Errorf("Boris = %+v", u.Notifications)
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
