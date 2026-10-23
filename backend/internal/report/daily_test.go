package report

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// Markers instead of the real phrases: the owner edits texts.yaml freely.
var reminderTexts = texts.Bot{
	Reminders: texts.Reminders{
		Neutral: []string{"neutral {limit}, {days}, {payday}, {left}"},
		Full:    []string{"full {limit}"},
		Empty:   []string{"empty {payday}"},
		Hungry:  []string{"hungry {limit}"},
		Fed:     []string{"fed {limit}"},
	},
	ReminderNoIncome: "no income",
	ReminderPayment:  "pay {title} {amount}",
}

func setupDaily(t *testing.T) (*Daily, *[]sent, *bool) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 9, 30, 0, 0, moscow)
	ops := memory.NewOperationRepository()
	l := ledger.NewService(ops, moscow)
	store := memory.NewBudget(ops)
	store.Now = func() time.Time { return now.AddDate(0, -1, 0) } // set up a month ago
	svc := budget.NewService(store, l)
	n := decimal.NewFromInt
	income := func(user int64, sum int64, d time.Time) {
		if _, err := ops.Create(ctx, ledger.Operation{UserID: user, Amount: n(sum), Category: ledger.CategorySalary, Date: d}); err != nil {
			t.Fatal(err)
		}
	}
	plan := func(user int64, hour int, on bool) {
		if _, err := svc.SetPlan(ctx, budget.Plan{UserID: user, Paydays: []int{5}, ReminderHour: hour, Reminders: on}); err != nil {
			t.Fatal(err)
		}
	}
	payment := func(user int64, title string, sum int64, day int) {
		if _, err := svc.CreatePayment(ctx, budget.RegularPayment{UserID: user, Title: title, Amount: n(sum), Day: day}); err != nil {
			t.Fatal(err)
		}
	}

	// Friday, October 16: payday on the 5th leaves 20 days, tomorrow is the 17th.
	plan(1, 9, true) // Anna: 26 000 − internet 600 over 20 days
	income(1, 26000, day(10, 5))
	payment(1, "Интернет", 600, 17)
	payment(2, "Аренда", 45000, 17) // Boris: no plan, only a payment due tomorrow
	plan(3, 9, false)               // Vera turned reminders off
	payment(3, "Связь", 300, 17)
	plan(4, 10, true) // Gosha: not before 10:00
	income(4, 26000, day(10, 5))
	plan(5, 9, true)               // Dasha has not accepted the policy
	plan(6, 9, true)               // Egor: no income since the payday
	payment(7, "Кружок", 2000, 25) // Zhenya: nothing due tomorrow
	plan(8, 9, true)               // Zoya went over yesterday's limit
	income(8, 20000, day(10, 5))
	if _, err := ops.Create(ctx, ledger.Operation{UserID: 8, Amount: n(5000), Category: ledger.CategoryShopping, Date: day(10, 15)}); err != nil {
		t.Fatal(err)
	}

	consented := now.AddDate(0, -1, 0)
	members := fakeMembers{}
	for id := int64(1); id <= 8; id++ {
		m := user.User{TelegramID: id, ConsentedAt: &consented}
		if id == 5 {
			m.ConsentedAt = nil
		}
		members = append(members, m)
	}
	var out []sent
	fail := false
	send := func(_ context.Context, chat int64, text string) error {
		if fail {
			return errors.New("telegram is down")
		}
		out = append(out, sent{chat, strings.ReplaceAll(text, " ", " ")})
		return nil
	}
	d := NewDaily(svc, members, &fakeRuns{claimed: map[string]bool{}}, send, reminderTexts, moscow,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.now = func() time.Time { return now }
	d.pick = func(int) int { return 0 }
	return d, &out, &fail
}

func TestDailyReminders(t *testing.T) {
	d, out, _ := setupDaily(t)
	d.Tick(context.Background())

	want := map[int64]string{
		1: "neutral 1 270 ₽, 20 дней, 5 ноября, 25 400 ₽\n\npay Интернет 600 ₽",
		2: "pay Аренда 45 000 ₽",
		6: "no income",
		8: "hungry 750 ₽",
	}
	got := map[int64]string{}
	for _, s := range *out {
		got[s.chat] = s.text
	}
	if len(got) != len(want) || len(*out) != len(want) {
		t.Errorf("sent to %v, want exactly %v", got, want)
	}
	for chat, text := range want {
		if got[chat] != text {
			t.Errorf("reminder to %d = %q, want %q", chat, got[chat], text)
		}
	}
}

func TestDailyReminderIsSentOnceAndRetriedOnFailure(t *testing.T) {
	d, out, fail := setupDaily(t)
	ctx := context.Background()
	*fail = true
	d.Tick(ctx)
	*fail = false
	d.Tick(ctx) // the failed claims were released: retried now
	d.Tick(ctx) // already sent: nothing new
	if len(*out) != 4 {
		t.Errorf("sent %d reminders, want 4: %+v", len(*out), *out)
	}

	// At 10:00 Gosha's hour comes; the next day everyone is due again.
	d.now = func() time.Time { return time.Date(2026, 10, 16, 10, 0, 0, 0, moscow) }
	d.Tick(ctx)
	if len(*out) != 5 || (*out)[4].chat != 4 {
		t.Errorf("at 10:00 = %+v, want Gosha's reminder", *out)
	}
	d.now = func() time.Time { return time.Date(2026, 10, 17, 9, 0, 0, 0, moscow) }
	d.Tick(ctx)
	if len(*out) < 8 {
		t.Errorf("next morning sent %d in total, want the plans reminded again", len(*out))
	}
}
