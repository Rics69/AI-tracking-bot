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
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/card"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

type gameEnv struct {
	g      *Game
	play   *game.Service
	store  *memory.Game
	ops    *ledger.Service
	repo   *memory.OperationRepository
	out    []sent
	cards  []card.Card
	now    time.Time
	rows   [][]game.Action
	failed bool
}

// setupGame is a game with one user, at noon of a Wednesday in Moscow.
func setupGame(t *testing.T, m user.User) *gameEnv {
	t.Helper()
	tx, err := texts.Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := game.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	e := &gameEnv{now: time.Date(2026, 10, 14, 12, 0, 0, 0, moscow), repo: memory.NewOperationRepository(), store: memory.NewGame()}
	e.store.Operations = e.repo
	e.ops = ledger.NewService(e.repo, moscow)
	b := budget.NewService(memory.NewBudget(e.repo), e.ops)
	e.play = game.NewService(catalog, e.store, e.ops, b)
	consented := e.now.AddDate(-1, 0, 0)
	m.TelegramID, m.ConsentedAt = 1, &consented
	say := func(_ context.Context, chat int64, text string, rows [][]game.Action) error {
		if e.failed {
			return errors.New("telegram is down")
		}
		e.out = append(e.out, sent{chat, text})
		e.rows = rows
		return nil
	}
	cards := func(_ context.Context, chat int64, c card.Card, caption string, _ [][]game.Action) error {
		e.cards = append(e.cards, c)
		e.out = append(e.out, sent{chat, caption})
		return nil
	}
	e.g = NewGame(e.play, fakeMembers{m}, &fakeRuns{claimed: map[string]bool{}}, say, cards, tx, moscow,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.g.now = func() time.Time { return e.now }
	return e
}

func (e *gameEnv) today() time.Time { return dateOfTime(e.now) }

func (e *gameEnv) spend(t *testing.T) {
	t.Helper()
	d := e.today()
	e.repo.SetClock(e.now)
	if _, err := e.ops.Create(context.Background(), ledger.CreateInput{UserID: 1, Amount: decimal.NewFromInt(100),
		Category: ledger.CategoryFood, Date: &d, Comment: "кофе", Today: d}); err != nil {
		t.Fatal(err)
	}
}

func TestRecordKeepsTheStreak(t *testing.T) {
	ctx := context.Background()
	e := setupGame(t, user.User{})
	e.spend(t)
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if p, _ := e.play.Profile(ctx, 1); p.Streak != 1 {
		t.Fatalf("a record through any door counts: %+v", p)
	}
	for range 6 {
		e.now = e.now.AddDate(0, 0, 1)
		e.spend(t)
		e.g.Tick(ctx)
	}
	if p, _ := e.play.Profile(ctx, 1); p.Streak != 7 || p.XP != 20 {
		t.Fatalf("after a week = %+v", p)
	}
	if len(e.cards) != 1 || e.cards[0].Value != "7 дней" {
		t.Errorf("the mark comes with a card: %+v", e.cards)
	}
}

func TestRecordJustBeforeMidnightCountsForItsDay(t *testing.T) {
	ctx := context.Background()
	e := setupGame(t, user.User{})
	e.now = time.Date(2026, 10, 14, 23, 58, 0, 0, moscow)
	e.spend(t)
	e.now = e.now.Add(5 * time.Minute)
	e.g.Tick(ctx)
	p, _ := e.play.Profile(ctx, 1)
	if p.LastDay == nil || !p.LastDay.Equal(day(10, 14)) {
		t.Errorf("profile = %+v", p)
	}
}

func TestStreakNudge(t *testing.T) {
	ctx := context.Background()
	e := setupGame(t, user.User{Notifications: user.Notifications{StreakReminder: true}})
	e.spend(t)
	e.g.Tick(ctx)

	// The next evening nothing is recorded yet.
	e.now = e.now.AddDate(0, 0, 1).Add(8 * time.Hour) // 20:00
	e.g.Tick(ctx)
	if len(e.out) != 0 {
		t.Fatalf("before 21:00: %+v", e.out)
	}
	e.now = e.now.Add(time.Hour)
	e.failed = true
	e.g.Tick(ctx) // not delivered: tried again
	e.failed = false
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.out) != 1 || !strings.Contains(e.out[0].text, "серия из 1 день") {
		t.Fatalf("nudge = %+v", e.out)
	}
	if len(e.rows) != 1 || e.rows[0][0].Data != game.ActNoSpend {
		t.Errorf("buttons = %+v", e.rows)
	}
}

func TestNoNudgeWhenTheDayCounts(t *testing.T) {
	ctx := context.Background()
	for name, tt := range map[string]struct {
		notify bool
		spend  bool
	}{"recorded today": {true, true}, "turned off": {false, false}} {
		t.Run(name, func(t *testing.T) {
			e := setupGame(t, user.User{Notifications: user.Notifications{StreakReminder: tt.notify}})
			e.spend(t)
			e.g.Tick(ctx)
			e.now = e.now.AddDate(0, 0, 1).Add(9 * time.Hour)
			if tt.spend {
				e.spend(t)
			}
			e.g.Tick(ctx)
			if len(e.out) != 0 {
				t.Errorf("sent = %+v", e.out)
			}
		})
	}
	e := setupGame(t, user.User{Notifications: user.Notifications{StreakReminder: true}})
	e.now = e.now.Add(9 * time.Hour)
	e.g.Tick(ctx)
	if len(e.out) != 0 {
		t.Errorf("no streak, nothing to save: %+v", e.out)
	}
}

func TestAutomaticQuestIsAnnounced(t *testing.T) {
	ctx := context.Background()
	e := setupGame(t, user.User{})
	if err := e.store.OfferQuest(ctx, 1, "log-three", e.today().Format(time.DateOnly)); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		e.spend(t)
	}
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.out) != 1 || !strings.Contains(e.out[0].text, "+10 опыта") {
		t.Errorf("sent = %+v", e.out)
	}
}

func TestLevelUpCard(t *testing.T) {
	ctx := context.Background()
	e := setupGame(t, user.User{})
	day := e.now
	for i := range 10 {
		e.now = day.AddDate(0, 0, -i)
		e.spend(t)
	}
	e.now = day
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.cards) != 1 || e.cards[0].Title != "Под контролем" {
		t.Fatalf("cards = %+v", e.cards)
	}
	if p, _ := e.play.Profile(ctx, 1); p.Level != 1 {
		t.Errorf("profile = %+v", p)
	}
}

func TestChallengeEveningAndFinish(t *testing.T) {
	ctx := context.Background()
	e := setupGame(t, user.User{})
	const slug = "no-spend-week"
	if _, _, err := e.play.Join(ctx, slug, 1, e.today()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.play.MarkDay(ctx, slug, 1, 1, game.DayLater, e.today()); err != nil {
		t.Fatal(err)
	}
	e.g.Tick(ctx)
	if len(e.out) != 0 {
		t.Fatalf("at noon: %+v", e.out)
	}
	e.now = e.now.Add(8 * time.Hour) // 20:00
	e.g.Tick(ctx)
	e.g.Tick(ctx)
	if len(e.out) != 1 || !strings.Contains(e.out[0].text, "Запишите сегодня все траты") {
		t.Fatalf("evening = %+v", e.out)
	}
	if len(e.rows) != 1 || len(e.rows[0]) != 2 {
		t.Errorf("no «later» in the evening: %+v", e.rows)
	}

	e.now = e.now.AddDate(0, 0, 7)
	e.g.Tick(ctx)
	if len(e.cards) != 1 || e.cards[0].Value != "0%" || e.cards[0].Title != "Неделя осознанных трат" {
		t.Fatalf("finish = %+v", e.cards)
	}
	if !strings.Contains(e.out[len(e.out)-1].text, "пройден: 0 из 7") {
		t.Errorf("caption = %+v", e.out)
	}
}

func TestMorningWithTheGame(t *testing.T) {
	ctx := context.Background()
	d, _, _, out := morning(t, user.User{})
	tx, err := texts.Load()
	if err != nil {
		t.Fatal(err)
	}
	d.t = tx
	repo := memory.NewOperationRepository()
	store := memory.NewGame()
	catalog, err := game.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	l := ledger.NewService(repo, moscow)
	play := game.NewService(catalog, store, l, budget.NewService(memory.NewBudget(repo), l))
	today := day(10, 16)
	if _, _, err := play.Join(ctx, "no-spend-week", 1, today); err != nil {
		t.Fatal(err)
	}
	var rows [][]game.Action
	d.Extras = GameMorning{Game: play, Texts: tx}
	d.SendRows = func(_ context.Context, chat int64, text string, r [][]game.Action) error {
		*out = append(*out, sent{chat, strings.ReplaceAll(text, "\u00a0", " ")})
		rows = r
		return nil
	}
	d.Tick(ctx)
	if len(*out) != 1 {
		t.Fatalf("one message in the morning: %+v", *out)
	}
	text := (*out)[0].text
	for _, want := range []string{"Цель «Ноутбук»", "день 1 из 7", "Квест дня"} {
		if !strings.Contains(text, want) {
			t.Errorf("message lacks %q:\n%s", want, text)
		}
	}
	if len(rows) != 1 || len(rows[0]) != 3 {
		t.Errorf("buttons = %+v", rows)
	}
}

func TestWeeklyWithTheGame(t *testing.T) {
	tx, err := texts.Load()
	if err != nil {
		t.Fatal(err)
	}
	w := &Weekly{Texts: tx}
	n := decimal.NewFromInt
	goal := &budget.Goal{Title: "Ноутбук", Target: n(1000), Saved: n(400)}
	spent, before := n(100), n(200)

	lines := w.blocks(game.Week{Goal: goal, GoalAdded: n(50), DaysUnder: 5, DaysCounted: 7, Streak: 3, QuestsDone: 2}, spent, before, true)
	text := strings.ReplaceAll(strings.Join(lines, "\n"), "\u00a0", " ")
	for _, want := range []string{"Цель «Ноутбук»: 40%, за неделю отложено 50 ₽.", "В лимите: 5 из 7 дней.", "Серия: 3 дня.", "Квестов выполнено: 2.", tx.WeekSpentLess} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}

	close := &budget.Goal{Title: "Ноутбук", Target: n(1000), Saved: n(900)}
	for name, tt := range map[string]struct {
		info          game.Week
		spent, before decimal.Decimal
		comparable    bool
		want          string
	}{
		"the goal first":  {game.Week{Goal: close, StreakBroken: true}, spent, before, true, texts.Fill(tx.WeekGoalClose, "title", "Ноутбук")},
		"a broken streak": {game.Week{Goal: goal, StreakBroken: true}, spent, before, true, tx.WeekStreakBroken},
		"less":            {game.Week{}, spent, before, true, tx.WeekSpentLess},
		"more":            {game.Week{}, before, spent, true, tx.WeekSpentMore},
		"no comparison":   {game.Week{}, spent, before, false, ""},
		"the same":        {game.Week{}, spent, spent, true, ""},
	} {
		if got := conclusion(tx, tt.info, tt.spent, tt.before, tt.comparable); got != tt.want {
			t.Errorf("%s: conclusion = %q, want %q", name, got, tt.want)
		}
	}
}
