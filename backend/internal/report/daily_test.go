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
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
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
	ReminderGoal:     "goal {title} {percent} {saved} {target}",
	ReminderRates:    "rates {rates} on {date}",
	Comeback:         "come back",
	ComebackGoal:     "come back to {title} {percent}",
	GoalProgress:     "progress {title} {percent} {saved} {target}",
	GoalReached:      "reached {title} {target}",
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
	settings := map[int64]user.Notifications{}
	plan := func(id int64, hour int, on bool) {
		settings[id] = user.Notifications{ReminderHour: hour, Reminders: on}
		if _, err := svc.SetPlan(ctx, budget.Plan{UserID: id, Paydays: []int{5}}); err != nil {
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
		m := user.User{TelegramID: id, ConsentedAt: &consented,
			Notifications: user.Notifications{ReminderHour: 9, Reminders: true}}
		if n, ok := settings[id]; ok {
			m.Notifications = n
		}
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
	d := NewDaily(svc, members, &fakeRuns{claimed: map[string]bool{}}, nil, send, reminderTexts, moscow,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.now = func() time.Time { return now }
	d.pick = func(int) int { return 0 }
	return d, &out, &fail
}

func TestDailyReminders(t *testing.T) {
	d, out, _ := setupDaily(t)
	d.Tick(context.Background())

	want := map[int64]string{
		1: "neutral 1 270 ₽, 20 дней, 5 ноября, 25 400 ₽\npay Интернет 600 ₽",
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

// morning builds a Daily around one user with a goal.
func morning(t *testing.T, m user.User) (*Daily, *memory.Budget, *memory.Rates, *[]sent) {
	t.Helper()
	ops := memory.NewOperationRepository()
	store := memory.NewBudget(ops)
	svc := budget.NewService(store, ledger.NewService(ops, moscow))
	now := time.Date(2026, 10, 16, 9, 30, 0, 0, moscow)
	consented := now.AddDate(-1, 0, 0)
	m.TelegramID, m.ConsentedAt = 1, &consented
	if m.Notifications == (user.Notifications{}) {
		m.Notifications = user.Notifications{ReminderHour: 9, Reminders: true, ShowRates: true}
	}
	r := memory.NewRates()
	var out []sent
	d := NewDaily(svc, fakeMembers{m}, &fakeRuns{claimed: map[string]bool{}}, r,
		func(_ context.Context, chat int64, text string) error {
			out = append(out, sent{chat, strings.ReplaceAll(text, "\u00a0", " ")})
			return nil
		}, reminderTexts, moscow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.now = func() time.Time { return now }
	if _, err := svc.CreateGoal(context.Background(), budget.Goal{UserID: 1, Title: "Ноутбук", Target: decimal.NewFromInt(120000)},
		day(10, 16)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Deposit(context.Background(), 1, 1, decimal.NewFromInt(48000), nil, day(10, 16)); err != nil {
		t.Fatal(err)
	}
	return d, store, r, &out
}

func TestMorningWithAGoalAndRates(t *testing.T) {
	ctx := context.Background()
	var r *memory.Rates
	d, _, _, out := morning(t, user.User{})
	d.Tick(ctx)
	if len(*out) != 1 || (*out)[0].text != "goal Ноутбук 40 48 000 ₽ 120 000 ₽" {
		t.Fatalf("no plan and no rates yet: %+v", *out)
	}

	d, _, r, out = morning(t, user.User{})
	_ = r.Save(ctx, []rates.Rate{
		{Code: "EUR", Date: day(10, 15), Value: decimal.RequireFromString("93.1")},
		{Code: "USD", Date: day(10, 16), Value: decimal.RequireFromString("80.5444")},
	})
	d.Tick(ctx)
	if len(*out) != 1 || (*out)[0].text != "goal Ноутбук 40 48 000 ₽ 120 000 ₽\nrates $ 80,54 ₽ · € 93,10 ₽ on 16 октября" {
		t.Errorf("message = %+v", *out)
	}

	d, _, r, out = morning(t, user.User{Notifications: user.Notifications{ReminderHour: 9, Reminders: true}})
	_ = r.Save(ctx, []rates.Rate{{Code: "USD", Date: day(10, 16), Value: decimal.NewFromInt(80)}})
	d.Tick(ctx)
	if len(*out) != 1 || strings.Contains((*out)[0].text, "rates") {
		t.Errorf("rates turned off: %+v", *out)
	}
}

func TestMorningFadesForThoseWhoLeft(t *testing.T) {
	ctx := context.Background()
	seen := func(daysAgo int) user.User {
		at := time.Date(2026, 10, 16, 12, 0, 0, 0, moscow).AddDate(0, 0, -daysAgo)
		return user.User{LastSeenAt: &at}
	}
	for daysAgo, want := range map[int]int{0: 1, 6: 1, 7: 1, 8: 0, 9: 0, 10: 1, 13: 1, 14: 0, 29: 0} {
		d, _, _, out := morning(t, seen(daysAgo))
		d.Tick(ctx)
		if len(*out) != want {
			t.Errorf("%d days away: sent %d, want %d", daysAgo, len(*out), want)
		}
	}

	d, _, _, out := morning(t, seen(30))
	d.Tick(ctx)
	if len(*out) != 1 || (*out)[0].text != "come back to Ноутбук 40" {
		t.Fatalf("after 30 days = %+v", *out)
	}
	now := d.now()
	d.now = func() time.Time { return now.AddDate(0, 0, 3) }
	d.Tick(ctx)
	if len(*out) != 1 {
		t.Errorf("the last message went out twice: %+v", *out)
	}
}

func TestGoalProgress(t *testing.T) {
	ctx := context.Background()
	ops := memory.NewOperationRepository()
	svc := budget.NewService(memory.NewBudget(ops), ledger.NewService(ops, moscow))
	today := day(10, 16)
	n := decimal.NewFromInt
	consented := today
	members := fakeMembers{
		{TelegramID: 1, ConsentedAt: &consented, Notifications: user.Notifications{GoalUpdates: true}},
		{TelegramID: 2, ConsentedAt: &consented}, // turned it off
	}
	for id := int64(1); id <= 2; id++ {
		if _, err := svc.CreateGoal(ctx, budget.Goal{UserID: id, Title: "Цель", Target: n(1000)}, today); err != nil {
			t.Fatal(err)
		}
	}
	deposit := func(goal int64, user int64, sum int64) {
		t.Helper()
		if _, err := svc.Deposit(ctx, user, goal, n(sum), nil, today); err != nil {
			t.Fatal(err)
		}
	}
	var out []sent
	fail := false
	p := NewGoalProgress(svc, members, &fakeRuns{claimed: map[string]bool{}},
		func(_ context.Context, chat int64, text string) error {
			if fail {
				return errors.New("telegram is down")
			}
			out = append(out, sent{chat, strings.ReplaceAll(text, "\u00a0", " ")})
			return nil
		}, reminderTexts, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.Tick(ctx)
	if len(out) != 0 {
		t.Fatalf("nothing saved yet: %+v", out)
	}
	deposit(1, 1, 300)
	deposit(2, 2, 900)
	fail = true
	p.Tick(ctx) // not delivered: retried
	fail = false
	p.Tick(ctx)
	p.Tick(ctx)
	if len(out) != 1 || out[0].chat != 1 || out[0].text != "progress Цель 30 300 ₽ 1 000 ₽" {
		t.Fatalf("at 30%% = %+v", out)
	}

	// Taken back and saved again: not a new milestone.
	deposit(1, 1, -200)
	p.Tick(ctx)
	deposit(1, 1, 200)
	p.Tick(ctx)
	if len(out) != 1 {
		t.Errorf("repeated a milestone: %+v", out)
	}

	// A jump over two levels is one message about the highest.
	deposit(1, 1, 500)
	p.Tick(ctx)
	if len(out) != 2 || out[1].text != "progress Цель 80 800 ₽ 1 000 ₽" {
		t.Fatalf("at 80%% = %+v", out)
	}
	p.Tick(ctx)
	deposit(1, 1, 200)
	p.Tick(ctx)
	if len(out) != 3 || out[2].text != "reached Цель 1 000 ₽" {
		t.Errorf("at 100%% = %+v", out)
	}
}

type fakeSpending struct {
	spent int64
	err   error
}

func (f *fakeSpending) Spent(context.Context) (int64, error) { return f.spent, f.err }

func TestPackageAlert(t *testing.T) {
	ctx := context.Background()
	tx, err := texts.Load()
	if err != nil {
		t.Fatal(err)
	}
	spending := &fakeSpending{}
	runs := &fakeRuns{claimed: map[string]bool{}}
	var sent []string
	failing := false
	send := func(_ context.Context, chatID int64, text string) error {
		if chatID != 99 {
			t.Errorf("sent to %d, want the owner", chatID)
		}
		if failing {
			return errors.New("network")
		}
		sent = append(sent, text)
		return nil
	}
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	alert := NewPackageAlert(spending, runs, send, 99, 2500000, since, tx, slog.New(slog.NewTextHandler(io.Discard, nil)))

	spending.spent = 1249999
	alert.Tick(ctx)
	if len(sent) != 0 {
		t.Fatalf("below 50%%: %q", sent)
	}

	spending.spent = 1300000
	failing = true
	alert.Tick(ctx) // not delivered: the next tick tries again
	failing = false
	alert.Tick(ctx)
	alert.Tick(ctx)
	if len(sent) != 1 || !strings.Contains(sent[0], "52%") || !strings.Contains(sent[0], "1 300 000 из 2 500 000") ||
		!strings.Contains(sent[0], "осталось 1 200 000") {
		t.Fatalf("at 52%%: %q", sent)
	}

	spending.spent = 2400000 // 96%: both levels passed since the last tick
	alert.Tick(ctx)
	if len(sent) != 3 {
		t.Fatalf("at 96%%: %q", sent)
	}

	spending.spent = 2600000
	alert.Tick(ctx)
	if len(sent) != 4 || !strings.Contains(sent[3], "закончился") {
		t.Fatalf("at the end: %q", sent)
	}
}
