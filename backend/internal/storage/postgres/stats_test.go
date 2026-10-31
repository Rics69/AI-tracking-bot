package postgres

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/stats"
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

	if total, err := u.Total(ctx, day(23)); err != nil || total != 160 {
		t.Errorf("tokens since the 23rd = %d, %v", total, err) // cached ones are free
	}
	ev := NewEventRepository(testPool)
	for _, e := range []struct {
		id   int64
		name string
		via  string
	}{
		{1, events.ExpenseAdded, events.ViaFast},
		{1, events.ExpenseAdded, events.ViaFast},
		{1, events.IncomeAdded, events.ViaAI},
		{2, events.ExpenseAdded, events.ViaMiniApp},
	} {
		if err := ev.Add(ctx, e.id, e.name, events.Props{"via": e.via}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ev.Add(ctx, 0, events.Start, events.Props{"source": "reels"}); err != nil {
		t.Fatalf("an event of nobody: %v", err)
	}
	if _, err := users.SetSource(ctx, 1, "reels"); err != nil {
		t.Fatal(err)
	}

	s, err := NewStatsRepository(testPool).Snapshot(ctx, time.Now(), day(24), day(1), day(23))
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
	if s.Package.Spent != 160 || s.Package.WeekTokens != 160 || s.Package.WeekDays != 2 {
		t.Errorf("package = %+v", s.Package)
	}
	if s.Recorded != (stats.Recorded{Fast: 2, AI: 1, MiniApp: 1}) {
		t.Errorf("recorded = %+v", s.Recorded)
	}
	if !slices.Equal(s.Sources, []stats.SourceCount{{Name: "direct", Users: 1}, {Name: "reels", Users: 1}}) {
		t.Errorf("sources = %+v", s.Sources)
	}
}

func TestSourcesAndPendingStart(t *testing.T) {
	_, users := setup(t)
	ctx := context.Background()
	u, err := users.SetPendingStart(ctx, 1, "go_dream__reels")
	if err != nil || u.PendingStart != "go_dream__reels" {
		t.Fatalf("pending = %+v, %v", u, err)
	}
	if u, err = users.SetSource(ctx, 1, "reels"); err != nil || u.FirstSource != "reels" || u.LastSource != "reels" {
		t.Fatalf("first source = %+v, %v", u, err)
	}
	if u, err = users.SetSource(ctx, 1, "tg"); err != nil || u.FirstSource != "reels" || u.LastSource != "tg" {
		t.Errorf("second source = %+v, %v", u, err)
	}
	if u, err = users.SetPendingStart(ctx, 1, ""); err != nil || u.PendingStart != "" {
		t.Errorf("cleared = %+v, %v", u, err)
	}
	if u, err = users.Get(ctx, 1); err != nil || u.LastSource != "tg" {
		t.Errorf("reloaded = %+v, %v", u, err)
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
