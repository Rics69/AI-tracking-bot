package report

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

var moscow, _ = time.LoadLocation("Europe/Moscow")

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }

func TestReportWeek(t *testing.T) {
	tests := []struct {
		now  time.Time
		week time.Time
		due  bool
	}{
		{time.Date(2026, 10, 19, 9, 59, 0, 0, moscow), day(10, 12), false}, // Monday before 10:00
		{time.Date(2026, 10, 19, 10, 0, 0, 0, moscow), day(10, 12), true},  // Monday 10:00
		{time.Date(2026, 10, 21, 3, 0, 0, 0, moscow), day(10, 12), true},   // catch-up on Wednesday
		{time.Date(2026, 10, 25, 23, 0, 0, 0, moscow), day(10, 12), true},  // Sunday: still last week
	}
	for _, tt := range tests {
		week, due := reportWeek(tt.now)
		if !week.Equal(tt.week) || due != tt.due {
			t.Errorf("reportWeek(%s) = %s %v, want %s %v", tt.now, week.Format(time.DateOnly), due, tt.week.Format(time.DateOnly), tt.due)
		}
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[string]string{
		"350": "350 ₽", "12450": "12 450 ₽", "1249.9": "1 249,90 ₽", "1000000": "1 000 000 ₽",
	} {
		if got := strings.ReplaceAll(Money(decimal.RequireFromString(in)), "\u00a0", " "); got != want {
			t.Errorf("Money(%s) = %q, want %q", in, got, want)
		}
	}
}

type fakeRuns struct {
	claimed map[string]bool
}

func (f *fakeRuns) Claim(_ context.Context, week time.Time, id int64) (bool, error) {
	k := week.Format(time.DateOnly) + string(rune(id))
	if f.claimed[k] {
		return false, nil
	}
	f.claimed[k] = true
	return true, nil
}

func (f *fakeRuns) Release(_ context.Context, week time.Time, id int64) error {
	delete(f.claimed, week.Format(time.DateOnly)+string(rune(id)))
	return nil
}

type fakeMembers []user.User

func (f fakeMembers) All(context.Context) ([]user.User, error) { return f, nil }

type sent struct {
	chat int64
	text string
}

func setup(t *testing.T) (*Weekly, *[]sent, *bool) {
	t.Helper()
	repo := memory.NewExpenseRepository()
	svc := expense.NewService(repo, moscow)
	add := func(user int64, sum string, c expense.Category, d time.Time, comment string) {
		if _, err := repo.Create(context.Background(), expense.Expense{
			UserID: user, Amount: decimal.RequireFromString(sum), Category: c, SpentAt: d, Comment: comment,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Week of Oct 12–18 for Anna, a smaller previous week, and an expense outside both.
	add(1, "45000", expense.CategoryHousing, day(10, 14), "аренда")
	add(1, "350", expense.CategoryFood, day(10, 18), "кофе")
	add(1, "2480.50", expense.CategoryFood, day(10, 12), "продукты")
	add(1, "1200", expense.CategoryTransport, day(10, 13), "такси")
	add(1, "10000", expense.CategoryFood, day(10, 7), "прошлая неделя")
	add(1, "999", expense.CategoryOther, day(10, 19), "уже эта неделя")
	add(4, "100", expense.CategoryFood, day(10, 14), "заблокирован")
	add(5, "100", expense.CategoryFood, day(10, 14), "без согласия")

	now := time.Date(2026, 10, 19, 10, 30, 0, 0, moscow)
	consented := now.AddDate(0, -1, 0)
	members := fakeMembers{
		{TelegramID: 1, FirstName: "Анна", WeeklyReport: true, ConsentedAt: &consented},
		{TelegramID: 2, FirstName: "Борис", WeeklyReport: true, ConsentedAt: &consented}, // no expenses: no message
		{TelegramID: 3, FirstName: "Вера", WeeklyReport: false, ConsentedAt: &consented},
		{TelegramID: 4, FirstName: "Гоша", WeeklyReport: true, ConsentedAt: &consented, BlockedAt: &consented},
		{TelegramID: 5, FirstName: "Даша", WeeklyReport: true}, // has not accepted the policy
	}
	var out []sent
	fail := false
	send := func(_ context.Context, chat int64, text string) error {
		if fail {
			return errors.New("telegram is down")
		}
		out = append(out, sent{chat, text})
		return nil
	}
	w := NewWeekly(svc, members, &fakeRuns{claimed: map[string]bool{}}, send, moscow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.now = func() time.Time { return now }
	return w, &out, &fail
}

func TestWeeklyReportContent(t *testing.T) {
	w, out, _ := setup(t)
	w.Tick(context.Background())

	if len(*out) != 1 || (*out)[0].chat != 1 {
		t.Fatalf("sent = %+v, want one report to Anna", *out)
	}
	text := strings.ReplaceAll((*out)[0].text, "\u00a0", " ")
	for _, want := range []string{
		"Итоги недели · 12–18 октября",
		"Потрачено 49 030,50 ₽ — 4 траты",
		"В 4,9 раза больше, чем неделей раньше (10 000 ₽).",
		"• жильё — 45 000 ₽",
		"• еда — 2 830,50 ₽",
		"Самые крупные:\n• аренда — 45 000 ₽, 14 октября\n• продукты — 2 480,50 ₽, 12 октября\n• такси — 1 200 ₽, 13 октября",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "уже эта неделя") {
		t.Error("report includes an expense from the current week")
	}
}

func TestWeeklyReportIsSentOnceAndRetriedOnFailure(t *testing.T) {
	w, out, fail := setup(t)
	ctx := context.Background()

	*fail = true
	w.Tick(ctx)
	if len(*out) != 0 {
		t.Fatal("sent while Telegram is down")
	}
	*fail = false
	w.Tick(ctx) // the failed claim was released: retried now
	w.Tick(ctx) // already sent: nothing new
	if len(*out) != 1 {
		t.Errorf("sent %d reports, want exactly 1", len(*out))
	}
}

func TestComparison(t *testing.T) {
	d := decimal.RequireFromString
	for _, tt := range []struct{ cur, prev, want string }{
		{"1500", "1000", "На 50% больше"},
		{"800", "1000", "На 20% меньше"},
		{"1000", "1000", "Столько же"},
		{"3000", "1000", "В 3 раза больше"},
		{"5000", "1000", "В 5 раз больше"},
		{"14800", "1000", "В 14,8 раза больше"},
	} {
		if got := comparison(d(tt.cur), d(tt.prev)); !strings.HasPrefix(got, tt.want) {
			t.Errorf("comparison(%s, %s) = %q, want prefix %q", tt.cur, tt.prev, got, tt.want)
		}
	}
}

func TestUndeliverableReportIsNotRetried(t *testing.T) {
	w, _, _ := setup(t)
	attempts := 0
	w.send = func(context.Context, int64, string) error {
		attempts++
		return fmt.Errorf("%w: chat not found", ErrUndeliverable)
	}
	w.Tick(context.Background())
	w.Tick(context.Background())
	if attempts != 1 {
		t.Errorf("send attempted %d times, want 1", attempts)
	}
}

func TestNoReportsBeforeMondayMorning(t *testing.T) {
	w, out, _ := setup(t)
	w.now = func() time.Time { return time.Date(2026, 10, 19, 9, 0, 0, 0, moscow) }
	w.Tick(context.Background())
	if len(*out) != 0 {
		t.Errorf("sent %d reports before 10:00 on Monday", len(*out))
	}
}

func TestReportGoesOutAtTenInTheUsersTimezone(t *testing.T) {
	w, out, _ := setup(t)
	members := w.members.(fakeMembers)
	members[0].Timezone = "Asia/Vladivostok" // UTC+10: 10:00 there is 03:00 in Moscow
	w.now = func() time.Time { return time.Date(2026, 10, 19, 3, 30, 0, 0, moscow) }
	w.Tick(context.Background())
	if len(*out) != 1 || (*out)[0].chat != 1 {
		t.Errorf("sent = %+v, want Anna's report: it is past 10:00 in Vladivostok", *out)
	}
}
