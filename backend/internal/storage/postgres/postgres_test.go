package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/shopspring/decimal"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// testPool is shared by all tests of the package; each test truncates the tables.
var testPool *pgxpool.Pool

// TestMain starts a throwaway PostgreSQL in Docker and applies the migrations.
// Run `go test -short` to skip these integration tests when Docker is unavailable.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() || os.Getenv("SKIP_DB_TESTS") != "" {
		os.Exit(0)
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		panic("start postgres container: " + err.Error())
	}
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(err)
	}
	testPool, err = Connect(ctx, url)
	if err != nil {
		panic(err)
	}
	if err := Migrate(ctx, testPool); err != nil {
		panic(err)
	}

	code := m.Run()
	testPool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

func setup(t *testing.T) (*ExpenseRepository, *UserRepository) {
	t.Helper()
	truncate(t)
	users := NewUserRepository(testPool)
	// Anna and Boris start in families of their own.
	for _, u := range []user.User{{TelegramID: 1, FirstName: "Анна"}, {TelegramID: 2, FirstName: "Борис"}} {
		if _, err := users.Admit(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	return NewExpenseRepository(testPool), users
}

func truncate(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		TRUNCATE expenses, invites, settings, chat_turns, report_runs, llm_usage, users, families RESTART IDENTITY`); err != nil {
		t.Fatal(err)
	}
}

// joinFamily moves member into head's family through an invite.
func joinFamily(t *testing.T, head, member int64) {
	t.Helper()
	ctx := context.Background()
	h, err := NewUserRepository(testPool).Get(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	invites := NewInviteRepository(testPool)
	hash := []byte(time.Now().String())
	if _, err := invites.Create(ctx, h.FamilyID, head, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := invites.Redeem(ctx, hash, member); err != nil {
		t.Fatal(err)
	}
}

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func amount(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func mustCreate(t *testing.T, r *ExpenseRepository, userID int64, sum string, c expense.Category, d int, comment string) expense.Expense {
	t.Helper()
	e, err := r.Create(context.Background(), expense.Expense{
		UserID: userID, Amount: amount(sum), Category: c, SpentAt: day(d), Comment: comment,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func ids(items []expense.Expense) []int64 {
	out := make([]int64, len(items))
	for i, e := range items {
		out[i] = e.ID
	}
	return out
}

func TestCreateAndGetRoundTrip(t *testing.T) {
	repo, _ := setup(t)
	ctx := context.Background()

	created := mustCreate(t, repo, 1, "350.50", expense.CategoryFood, 15, "кофе")
	got, err := repo.Get(ctx, expense.OwnedBy(1), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Amount.Equal(amount("350.50")) || got.Category != expense.CategoryFood ||
		!got.SpentAt.Equal(day(15)) || got.Comment != "кофе" || got.UserID != 1 {
		t.Errorf("got %+v", got)
	}

	noComment := mustCreate(t, repo, 1, "10", expense.CategoryOther, 15, "")
	if noComment.Comment != "" {
		t.Errorf("empty comment came back as %q", noComment.Comment)
	}
}

func TestCreateForUnknownUser(t *testing.T) {
	repo, _ := setup(t)
	_, err := repo.Create(context.Background(), expense.Expense{
		UserID: 999, Amount: amount("1"), Category: expense.CategoryOther, SpentAt: day(1),
	})
	if !errors.Is(err, expense.ErrUnknownUser) {
		t.Fatalf("err = %v, want ErrUnknownUser", err)
	}
}

func TestScopeIsolation(t *testing.T) {
	repo, _ := setup(t)
	ctx := context.Background()
	annas := mustCreate(t, repo, 1, "100", expense.CategoryFood, 10, "")
	mustCreate(t, repo, 2, "200", expense.CategoryFood, 10, "")

	borisScope := expense.OwnedBy(2)
	if _, err := repo.Get(ctx, borisScope, annas.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	annas.Amount = amount("1")
	if _, err := repo.Update(ctx, borisScope, annas); !errors.Is(err, expense.ErrNotFound) {
		t.Errorf("Update: err = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, borisScope, annas.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Errorf("Delete: err = %v, want ErrNotFound", err)
	}
	page, err := repo.List(ctx, borisScope, expense.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].UserID != 2 {
		t.Errorf("Boris sees %+v", page.Items)
	}
	// Even with an explicit filter for Anna, the scope wins.
	page, _ = repo.List(ctx, borisScope, expense.Filter{UserID: new(int64(1)), Limit: 10})
	if page.Total != 0 {
		t.Errorf("filter overrode scope: %+v", page.Items)
	}
	// The zero scope sees nothing.
	page, _ = repo.List(ctx, expense.Scope{}, expense.Filter{Limit: 10})
	if page.Total != 0 {
		t.Errorf("zero scope sees %d expenses", page.Total)
	}
	// Anna's expense is untouched.
	got, _ := repo.Get(ctx, expense.OwnedBy(1), annas.ID)
	if !got.Amount.Equal(amount("100")) {
		t.Errorf("Anna's amount changed to %s", got.Amount)
	}
}

func TestFamilyScope(t *testing.T) {
	repo, users := setup(t)
	ctx := context.Background()
	if _, err := users.Admit(ctx, user.User{TelegramID: 3, FirstName: "Вера"}); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, repo, 1, "100", expense.CategoryFood, 10, "")
	boris := mustCreate(t, repo, 2, "200", expense.CategoryFood, 10, "")
	veras := mustCreate(t, repo, 3, "300", expense.CategoryFood, 10, "")
	joinFamily(t, 1, 2)
	anna, _ := users.Get(ctx, 1)
	family := expense.InFamily(anna.FamilyID)

	page, err := repo.List(ctx, family, expense.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || !page.Sum.Equal(amount("300")) {
		t.Errorf("the head sees %+v, want Anna's and Boris's expenses", page.Items)
	}
	if _, err := repo.Get(ctx, family, veras.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Errorf("another family's expense: err = %v", err)
	}
	// The head cannot hand an expense to someone outside the family.
	boris.UserID = 3
	if _, err := repo.Update(ctx, family, boris); !errors.Is(err, expense.ErrNotFound) {
		t.Errorf("hand over to a stranger: err = %v", err)
	}
	boris.UserID = 1
	if moved, err := repo.Update(ctx, family, boris); err != nil || moved.UserID != 1 {
		t.Errorf("hand over within the family: %+v, %v", moved, err)
	}
	// Family 0 is the zero scope: nothing.
	if page, _ := repo.List(ctx, expense.InFamily(0), expense.Filter{Limit: 10}); page.Total != 0 {
		t.Errorf("family 0 sees %d", page.Total)
	}
}

func TestListFiltersAndTotals(t *testing.T) {
	repo, users := setup(t)
	ctx := context.Background()
	joinFamily(t, 1, 2)
	anna, _ := users.Get(ctx, 1)
	family := expense.InFamily(anna.FamilyID)
	coffee := mustCreate(t, repo, 1, "350", expense.CategoryFood, 14, "Кофе в зёрнах")
	taxi := mustCreate(t, repo, 1, "1200", expense.CategoryTransport, 13, "такси")
	lunch := mustCreate(t, repo, 2, "500.25", expense.CategoryFood, 15, "обед")
	old := mustCreate(t, repo, 1, "100", expense.CategoryFood, 1, "100%_скидка")

	tests := []struct {
		name string
		f    expense.Filter
		want []int64
		sum  string
	}{
		{"all, newest spent first", expense.Filter{}, []int64{lunch.ID, coffee.ID, taxi.ID, old.ID}, "2150.25"},
		{"by member", expense.Filter{UserID: new(int64(1))}, []int64{coffee.ID, taxi.ID, old.ID}, "1650"},
		{"by category", expense.Filter{Category: new(expense.CategoryFood)}, []int64{lunch.ID, coffee.ID, old.ID}, "950.25"},
		{"period inclusive", expense.Filter{From: new(day(13)), To: new(day(14))}, []int64{coffee.ID, taxi.ID}, "1550"},
		{"comment search is case-insensitive", expense.Filter{Query: "кофе"}, []int64{coffee.ID}, "350"},
		{"LIKE wildcards are literal", expense.Filter{Query: "%_"}, []int64{old.ID}, "100"},
		{"no matches", expense.Filter{Query: "самолёт"}, []int64{}, "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.f.Limit = 10
			page, err := repo.List(ctx, family, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(page.Items); !equalIDs(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
			if page.Total != len(tt.want) || !page.Sum.Equal(amount(tt.sum)) {
				t.Errorf("total = %d sum = %s, want %d and %s", page.Total, page.Sum, len(tt.want), tt.sum)
			}
		})
	}

	t.Run("paging keeps totals of the whole selection", func(t *testing.T) {
		page, err := repo.List(ctx, family, expense.Filter{Limit: 1, Offset: 1})
		if err != nil {
			t.Fatal(err)
		}
		if !equalIDs(ids(page.Items), []int64{coffee.ID}) || page.Total != 4 {
			t.Errorf("page = %v total = %d", ids(page.Items), page.Total)
		}
	})

	t.Run("sort by creation finds the last added", func(t *testing.T) {
		page, _ := repo.List(ctx, expense.OwnedBy(1), expense.Filter{Sort: expense.SortByCreatedAt, Limit: 1})
		if !equalIDs(ids(page.Items), []int64{old.ID}) {
			t.Errorf("last added = %v, want %d", ids(page.Items), old.ID)
		}
	})
}

func TestUpdateAndDelete(t *testing.T) {
	repo, _ := setup(t)
	ctx := context.Background()
	e := mustCreate(t, repo, 1, "350", expense.CategoryFood, 15, "кофе")

	e.Amount = amount("450")
	e.Comment = ""
	updated, err := repo.Update(ctx, expense.OwnedBy(1), e)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Amount.Equal(amount("450")) || updated.Comment != "" {
		t.Errorf("updated = %+v", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) && !updated.UpdatedAt.Equal(updated.CreatedAt) {
		t.Errorf("updated_at %s before created_at %s", updated.UpdatedAt, updated.CreatedAt)
	}

	if err := repo.Delete(ctx, expense.OwnedBy(1), e.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, expense.OwnedBy(1), e.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
}

func TestSummary(t *testing.T) {
	repo, _ := setup(t)
	ctx := context.Background()
	mustCreate(t, repo, 1, "350", expense.CategoryFood, 14, "")
	mustCreate(t, repo, 1, "150.50", expense.CategoryFood, 15, "")
	mustCreate(t, repo, 1, "1200", expense.CategoryTransport, 15, "")
	mustCreate(t, repo, 2, "9999", expense.CategoryHousing, 15, "")

	sum, err := repo.Summary(ctx, expense.OwnedBy(1), expense.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Items) != 2 ||
		sum.Items[0].Category != expense.CategoryTransport || !sum.Items[0].Sum.Equal(amount("1200")) ||
		sum.Items[1].Category != expense.CategoryFood || !sum.Items[1].Sum.Equal(amount("500.50")) || sum.Items[1].Count != 2 {
		t.Errorf("items = %+v", sum.Items)
	}
	if !sum.Sum.Equal(amount("1700.50")) || sum.Count != 3 {
		t.Errorf("sum = %s count = %d", sum.Sum, sum.Count)
	}
}

func TestAdmit(t *testing.T) {
	_, users := setup(t)
	ctx := context.Background()
	anna, _ := users.Get(ctx, 1)
	boris, _ := users.Get(ctx, 2)
	if !anna.IsHead || !boris.IsHead || anna.FamilyID == boris.FamilyID || anna.LastSeenAt == nil {
		t.Errorf("anna = %+v, boris = %+v: want separate families, each its own head", anna, boris)
	}
	if anna.Consented() || !anna.WeeklyReport || anna.Timezone != "" {
		t.Errorf("defaults of a new user: %+v", anna)
	}
	again, err := users.Admit(ctx, user.User{TelegramID: 1, FirstName: "Аня", Username: "anya"})
	if err != nil {
		t.Fatal(err)
	}
	if again.FamilyID != anna.FamilyID || again.FirstName != "Аня" || again.Username != "anya" {
		t.Errorf("second admit = %+v", again)
	}
	list, _ := users.All(ctx)
	if len(list) != 2 {
		t.Errorf("users = %+v", list)
	}
}

func TestUserSettings(t *testing.T) {
	_, users := setup(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if u, err := users.SetConsent(ctx, 1, at); err != nil || !u.ConsentedAt.Equal(at) {
		t.Errorf("consent: %+v %v", u, err)
	}
	if u, err := users.SetTimezone(ctx, 1, "Asia/Omsk"); err != nil || u.Timezone != "Asia/Omsk" {
		t.Errorf("timezone: %+v %v", u, err)
	}
	if u, _ := users.SetTimezone(ctx, 1, ""); u.Timezone != "" {
		t.Errorf("reset timezone: %q", u.Timezone)
	}
	if _, err := users.SetConsent(ctx, 404, at); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("unknown user: err = %v", err)
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDetachTakesExpensesAlong(t *testing.T) {
	repo, users := setup(t)
	ctx := context.Background()
	mustCreate(t, repo, 2, "500", expense.CategoryFood, 10, "обед")
	joinFamily(t, 1, 2)
	anna, _ := users.Get(ctx, 1)
	if members, _ := users.Family(ctx, anna.FamilyID); len(members) != 2 || members[0].TelegramID != 1 {
		t.Fatalf("family = %+v, want Anna (head) first", members)
	}
	if page, _ := repo.List(ctx, expense.InFamily(anna.FamilyID), expense.Filter{}); page.Total != 1 {
		t.Fatalf("the head does not see Boris's expense")
	}

	boris, err := users.Detach(ctx, 2)
	if err != nil || !boris.IsHead || boris.FamilyID == anna.FamilyID {
		t.Fatalf("detached = %+v, %v", boris, err)
	}
	if page, _ := repo.List(ctx, expense.InFamily(anna.FamilyID), expense.Filter{}); page.Total != 0 {
		t.Error("the head still sees the expense of someone who left")
	}
	if page, _ := repo.List(ctx, expense.InFamily(boris.FamilyID), expense.Filter{}); page.Total != 1 {
		t.Error("Boris lost his expense")
	}
	if _, err := users.Detach(ctx, 404); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("unknown user: err = %v", err)
	}
}

func TestDeleteUser(t *testing.T) {
	repo, users := setup(t)
	ctx := context.Background()
	mustCreate(t, repo, 2, "500", expense.CategoryFood, 10, "обед")
	boris, _ := users.Get(ctx, 2)
	if err := NewUsageRepository(testPool).Add(ctx, 2, day(10), llm.Usage{PromptTokens: 10}, false); err != nil {
		t.Fatal(err)
	}
	if err := users.Delete(ctx, 2); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := testPool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM expenses WHERE user_id = 2) + (SELECT count(*) FROM llm_usage WHERE user_id = 2)
		     + (SELECT count(*) FROM families WHERE id = $1)`, boris.FamilyID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d rows of a deleted user are left", left)
	}
	if err := users.Delete(ctx, 2); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("second delete: err = %v", err)
	}
}

func TestInviteRedeem(t *testing.T) {
	_, users := setup(t)
	invites := NewInviteRepository(testPool)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)
	anna, _ := users.Get(ctx, 1)
	vera, _ := users.Admit(ctx, user.User{TelegramID: 3, FirstName: "Вера"})

	valid, _ := invites.Create(ctx, anna.FamilyID, 1, []byte("valid"), future)
	_, _ = invites.Create(ctx, anna.FamilyID, 1, []byte("expired"), time.Now().Add(-time.Minute))
	revoked, _ := invites.Create(ctx, anna.FamilyID, 1, []byte("revoked"), future)
	if err := invites.Revoke(ctx, vera.FamilyID, revoked.ID); !errors.Is(err, family.ErrInviteNotFound) {
		t.Errorf("revoke another family's invite: err = %v", err)
	}
	if err := invites.Revoke(ctx, anna.FamilyID, revoked.ID); err != nil {
		t.Fatal(err)
	}

	for _, hash := range []string{"expired", "revoked", "unknown"} {
		if _, err := invites.Redeem(ctx, []byte(hash), 3); !errors.Is(err, family.ErrInviteInvalid) {
			t.Errorf("%s: err = %v, want ErrInviteInvalid", hash, err)
		}
		if _, err := invites.Find(ctx, []byte(hash)); !errors.Is(err, family.ErrInviteInvalid) {
			t.Errorf("find %s: err = %v", hash, err)
		}
	}
	if found, err := invites.Find(ctx, []byte("valid")); err != nil || found.FamilyID != anna.FamilyID {
		t.Errorf("find valid: %+v %v", found, err)
	}
	if _, err := invites.Redeem(ctx, []byte("valid"), 1); !errors.Is(err, family.ErrAlreadyMember) {
		t.Errorf("the head redeems own invite: err = %v", err)
	}

	m, err := invites.Redeem(ctx, []byte("valid"), 3)
	if err != nil || m.FamilyID != anna.FamilyID || m.IsHead {
		t.Fatalf("redeem: %+v, %v", m, err)
	}
	var families int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM families WHERE id = $1`, vera.FamilyID).Scan(&families)
	if families != 0 {
		t.Error("Vera's empty family was not deleted")
	}
	if _, err := invites.Redeem(ctx, []byte("valid"), 2); !errors.Is(err, family.ErrInviteInvalid) {
		t.Errorf("second redeem: err = %v", err)
	}

	// A head whose family has members cannot leave it for another one.
	boris, _ := users.Get(ctx, 2)
	_, _ = invites.Create(ctx, boris.FamilyID, 2, []byte("to-boris"), future)
	if _, err := invites.Redeem(ctx, []byte("to-boris"), 1); !errors.Is(err, family.ErrHeadHasMembers) {
		t.Errorf("head with members: err = %v", err)
	}

	list, _ := invites.List(ctx, anna.FamilyID, time.Now().Add(-time.Hour))
	statuses := map[int64]family.InviteStatus{}
	for _, inv := range list {
		statuses[inv.ID] = inv.Status(time.Now())
	}
	if statuses[valid.ID] != family.InviteUsed || statuses[revoked.ID] != family.InviteRevoked || len(list) != 3 {
		t.Errorf("statuses = %v", statuses)
	}
	if err := invites.Revoke(ctx, anna.FamilyID, valid.ID); !errors.Is(err, family.ErrInviteNotFound) {
		t.Errorf("revoking a used invite: err = %v", err)
	}
}

func TestUsage(t *testing.T) {
	setup(t)
	ctx := context.Background()
	r := NewUsageRepository(testPool)
	if d, err := r.Get(ctx, 1, day(21)); err != nil || d.Requests != 0 {
		t.Fatalf("empty day: %+v %v", d, err)
	}
	_ = r.Add(ctx, 1, day(21), llm.Usage{PromptTokens: 100, CachedTokens: 900, CompletionTokens: 20}, false)
	_ = r.Add(ctx, 1, day(21), llm.Usage{PromptTokens: 50}, true)
	_ = r.Add(ctx, 1, day(22), llm.Usage{PromptTokens: 1}, false)
	d, err := r.Get(ctx, 1, day(21))
	if err != nil || d.Requests != 2 || d.Failures != 1 || d.Tokens() != 170 || d.CachedTokens != 900 {
		t.Errorf("day = %+v, %v", d, err)
	}
}

func TestSettings(t *testing.T) {
	setup(t)
	s := NewSettingsRepository(testPool)
	ctx := context.Background()
	if _, ok, err := s.Get(ctx, "missing"); ok || err != nil {
		t.Fatalf("missing key: ok=%v err=%v", ok, err)
	}
	for _, v := range []string{"FamilyBot", "NewBot"} {
		if err := s.Set(ctx, "bot", v); err != nil {
			t.Fatal(err)
		}
	}
	if v, ok, _ := s.Get(ctx, "bot"); !ok || v != "NewBot" {
		t.Errorf("value = %q", v)
	}
}

func TestChatHistory(t *testing.T) {
	setup(t)
	ctx := context.Background()
	h := NewChatHistoryRepository(testPool, 2, 30*time.Minute)
	turn := func(text string) []llm.Message {
		return []llm.Message{
			{Role: llm.RoleUser, Content: text},
			{Role: llm.RoleAssistant, StateID: "s1", ToolCall: &llm.ToolCall{Name: "add_expense", Arguments: json.RawMessage(`{"amount":350}`)}},
			{Role: llm.RoleTool, ToolName: "add_expense", Content: `{"added":{"id":1}}`},
			{Role: llm.RoleAssistant, Content: "Записано"},
		}
	}
	for _, text := range []string{"первый", "второй", "третий"} {
		if err := h.Append(ctx, 1, turn(text)); err != nil {
			t.Fatal(err)
		}
	}

	msgs, err := h.Messages(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Only the last 2 turns, oldest first, with tool calls intact.
	if len(msgs) != 8 || msgs[0].Content != "второй" || msgs[4].Content != "третий" {
		t.Fatalf("messages = %+v", msgs)
	}
	call := msgs[1]
	var args map[string]any
	if call.ToolCall != nil {
		_ = json.Unmarshal(call.ToolCall.Arguments, &args) // JSONB may reformat, the content must match
	}
	if call.ToolCall == nil || call.ToolCall.Name != "add_expense" || args["amount"] != 350.0 ||
		call.StateID != "s1" || msgs[2].ToolName != "add_expense" {
		t.Errorf("tool call round trip: %+v / %+v", call, msgs[2])
	}
	if other, _ := h.Messages(ctx, 2); len(other) != 0 {
		t.Errorf("history leaks between users: %d messages", len(other))
	}

	// An idle conversation starts from scratch.
	if _, err := testPool.Exec(ctx, `UPDATE chat_turns SET created_at = now() - interval '31 minutes'`); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := h.Messages(ctx, 1); len(msgs) != 0 {
		t.Errorf("expired conversation returned %d messages", len(msgs))
	}

	// Retention removes old turns; reset removes the rest.
	if err := h.Append(ctx, 1, turn("свежий")); err != nil {
		t.Fatal(err)
	}
	if n, err := h.DeleteOlderThan(ctx, time.Hour); err != nil || n != 0 {
		t.Errorf("deleted %d turns younger than an hour, err = %v", n, err)
	}
	if n, _ := h.DeleteOlderThan(ctx, 10*time.Minute); n != 3 {
		t.Errorf("deleted %d old turns, want 3", n)
	}
	if err := h.Reset(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := h.Messages(ctx, 1); len(msgs) != 0 {
		t.Errorf("after reset: %d messages", len(msgs))
	}
}

func TestWeeklyReportSettingAndRuns(t *testing.T) {
	_, users := setup(t)
	ctx := context.Background()

	anna, _ := users.Get(ctx, 1)
	if !anna.WeeklyReport {
		t.Error("weekly report must be on by default")
	}
	off, err := users.SetWeeklyReport(ctx, 1, false)
	if err != nil || off.WeeklyReport {
		t.Fatalf("set off: %+v, %v", off, err)
	}
	if _, err := users.SetWeeklyReport(ctx, 404, true); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("unknown user: err = %v", err)
	}

	runs := NewReportRunRepository(testPool)
	week := day(12)
	if ok, err := runs.Claim(ctx, week, 1); !ok || err != nil {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if ok, _ := runs.Claim(ctx, week, 1); ok {
		t.Error("the same report was claimed twice")
	}
	if ok, _ := runs.Claim(ctx, week, 2); !ok {
		t.Error("claims of different members must not conflict")
	}
	if err := runs.Release(ctx, week, 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := runs.Claim(ctx, week, 1); !ok {
		t.Error("a released report must be claimable again")
	}
}

func TestAggregateMatchesGoBuckets(t *testing.T) {
	repo, _ := setup(t)
	ctx := context.Background()
	mustCreate(t, repo, 1, "100", expense.CategoryFood, 11, "")      // Sunday
	mustCreate(t, repo, 1, "200", expense.CategoryFood, 12, "")      // Monday
	mustCreate(t, repo, 1, "300", expense.CategoryTransport, 18, "") // Sunday
	mustCreate(t, repo, 2, "999", expense.CategoryFood, 12, "")      // another member

	totals, err := repo.Aggregate(ctx, expense.OwnedBy(1), expense.Filter{}, expense.BucketWeek)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tt := range totals {
		got[tt.Start.Format(time.DateOnly)+" "+string(tt.Category)] = tt.Sum.String()
		// PostgreSQL's date_trunc and the Go helper must agree, or zero-filling breaks.
		if !tt.Start.Equal(expense.TruncateDate(tt.Start, expense.BucketWeek)) {
			t.Errorf("bucket %s is not a Monday", tt.Start)
		}
	}
	want := map[string]string{"2026-10-05 food": "100", "2026-10-12 food": "200", "2026-10-12 transport": "300"}
	if len(got) != len(want) {
		t.Fatalf("totals = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s (all: %v)", k, got[k], v, got)
		}
	}
}

// TestFamiliesMigrationKeepsExistingData replays migration 5 on data of the
// single-family version: the users of a live database must land correctly.
func TestFamiliesMigrationKeepsExistingData(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	db := stdlib.OpenDBFromPool(testPool)
	defer func() { _ = db.Close() }()
	dir, _ := fs.Sub(migrations, "migrations")
	provider, err := goose.NewProvider(goose.DialectPostgres, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	// Back to the latest schema whatever happens: other tests share the database.
	defer func() {
		if _, err := provider.Up(ctx); err != nil {
			t.Fatal(err)
		}
	}()

	_, err = testPool.Exec(ctx, `
		INSERT INTO users (telegram_id, first_name, is_head, removed_at, created_at) VALUES
		    (1, 'Анна',  true,  NULL,  now() - interval '3 days'),
		    (2, 'Борис', true,  NULL,  now() - interval '2 days'), -- a second head from ALLOWED_TELEGRAM_IDS
		    (3, 'Вера',  false, NULL,  now() - interval '1 day'),
		    (4, 'Гоша',  false, now(), now() - interval '1 day');  -- removed earlier
		INSERT INTO expenses (user_id, amount, category, spent_at) VALUES (4, 100, 'food', '2026-10-01');
		INSERT INTO invites (code_hash, expires_at) VALUES ('legacy', now() + interval '1 day');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}

	users := NewUserRepository(testPool)
	anna, _ := users.Get(ctx, 1)
	members, _ := users.Family(ctx, anna.FamilyID)
	if len(members) != 3 || !members[0].IsHead || members[0].TelegramID != 1 || members[1].IsHead || members[2].IsHead {
		t.Errorf("family = %+v, want Anna as the only head, Boris and Vera as members", members)
	}
	gosha, _ := users.Get(ctx, 4)
	if !gosha.IsHead || gosha.FamilyID == anna.FamilyID {
		t.Errorf("removed member = %+v, want a family of his own", gosha)
	}
	if page, _ := NewExpenseRepository(testPool).List(ctx, expense.InFamily(gosha.FamilyID), expense.Filter{}); page.Total != 1 {
		t.Error("the removed member lost his expense")
	}
	if anna.Consented() {
		t.Error("existing users must accept the data policy again")
	}
	if inv, err := NewInviteRepository(testPool).Find(ctx, []byte("legacy")); err != nil || inv.FamilyID != anna.FamilyID {
		t.Errorf("legacy invite = %+v, %v", inv, err)
	}
}
