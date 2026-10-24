package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func TestStatsSnapshot(t *testing.T) {
	ops, users := setup(t) // Anna and Boris, each in a family of their own
	ctx := context.Background()
	if _, err := users.Admit(ctx, user.User{TelegramID: 3, FirstName: "Вера"}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.SetConsent(ctx, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	joinFamily(t, 1, 2)
	// Vera joined long ago and has not been back for a month.
	if _, err := testPool.Exec(ctx, `UPDATE users SET created_at = now() - interval '40 days',
		last_seen_at = now() - interval '30 days', blocked_at = now() WHERE telegram_id = 3`); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, ops, 1, "350", ledger.CategoryFood, 20, "кофе")
	mustCreate(t, ops, 1, "80000", ledger.CategorySalary, 20, "")
	mustCreate(t, ops, 2, "500", ledger.CategoryFood, 21, "")
	b := NewBudgetRepository(testPool)
	if _, err := b.SavePlan(ctx, budget.Plan{UserID: 1, Paydays: []int{5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CreateGoal(ctx, budget.Goal{UserID: 2, Title: "Отпуск", Target: amount("1000")}); err != nil {
		t.Fatal(err)
	}
	u := NewUsageRepository(testPool)
	_ = u.Add(ctx, 1, day(24), llm.Usage{PromptTokens: 100, CachedTokens: 400, CompletionTokens: 10}, false, 2*time.Second)
	_ = u.Add(ctx, 2, day(24), llm.Usage{PromptTokens: 50}, true, time.Second)
	_ = u.Add(ctx, 1, day(3), llm.Usage{PromptTokens: 7}, false, time.Second)
	_ = u.Add(ctx, 1, day(23), llm.Usage{}, false, 0)

	s, err := NewStatsRepository(testPool).Snapshot(ctx, time.Now(), day(24), day(1))
	if err != nil {
		t.Fatal(err)
	}
	if s.Users != 3 || s.Consented != 1 || s.Blocked != 1 || s.NewDay != 2 || s.NewWeek != 2 || s.ActiveDay != 2 || s.ActiveWeek != 2 {
		t.Errorf("users = %+v", s)
	}
	if s.OperationsWeek != 3 || s.RecordersWeek != 2 || s.WithPlan != 1 || s.WithGoals != 1 || s.WithPayments != 0 ||
		s.Families != 1 || s.InFamilies != 2 {
		t.Errorf("activity = %+v", s)
	}
	if s.Today.Replies != 2 || s.Today.Failures != 1 || s.Today.PromptTokens != 150 || s.Today.CachedTokens != 400 ||
		s.Today.ReplyTime != 3*time.Second {
		t.Errorf("today = %+v", s.Today)
	}
	if s.Month.Replies != 4 || s.Month.PromptTokens != 157 {
		t.Errorf("month = %+v", s.Month)
	}
}

func TestSetBlocked(t *testing.T) {
	_, users := setup(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if u, err := users.SetBlocked(ctx, 1, &at); err != nil || !u.Blocked() || !u.BlockedAt.Equal(at) {
		t.Fatalf("blocked = %+v, %v", u, err)
	}
	if u, err := users.SetBlocked(ctx, 1, nil); err != nil || u.Blocked() {
		t.Errorf("unblocked = %+v, %v", u, err)
	}
}
