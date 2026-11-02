package game_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	. "github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
)

const anna int64 = 1

var (
	ctx   = context.Background()
	today = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) // a Wednesday
	at    = today.Add(12 * time.Hour)
)

type env struct {
	s       *Service
	store   *memory.Game
	ops     *ledger.Service
	budgets *budget.Service
}

func setup(t *testing.T) env {
	t.Helper()
	repo := memory.NewOperationRepository()
	l := ledger.NewService(repo, time.UTC)
	bs := memory.NewBudget(repo)
	bs.Now = func() time.Time { return today.AddDate(-1, 0, 0) }
	b := budget.NewService(bs, l)
	store := memory.NewGame()
	store.Operations = repo
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return env{s: NewService(c, store, l, b), store: store, ops: l, budgets: b}
}

func (e env) spend(t *testing.T, category ledger.Category, sum int64, comment string, day time.Time) {
	t.Helper()
	if _, err := e.ops.Create(ctx, ledger.CreateInput{UserID: anna, Amount: decimal.NewFromInt(sum), Category: category,
		Date: &day, Comment: comment, Today: day}); err != nil {
		t.Fatal(err)
	}
}

func always(day time.Time) func(int64) (time.Time, bool) {
	return func(int64) (time.Time, bool) { return day, true }
}

func TestCatalogLoads(t *testing.T) {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Quests) < 9 || len(c.Challenges) == 0 {
		t.Errorf("catalog = %d quests, %d challenges", len(c.Quests), len(c.Challenges))
	}
	for level := 0; level <= MaxLevel; level++ {
		found := false
		for _, q := range c.Quests {
			found = found || q.For(level) && q.Kind == KindDaily
		}
		if !found {
			t.Errorf("no quest of the day for level %d", level)
		}
	}
}

func TestCatalogChecksItself(t *testing.T) {
	for name, data := range map[string]string{
		"a bad slug":     `[{slug: "Плохой", title: a, text: b, levels: [0, 1], kind: daily, check: manual, xp: 1}]`,
		"no experience":  `[{slug: a, title: a, text: b, levels: [0, 1], kind: daily, check: manual}]`,
		"levels":         `[{slug: a, title: a, text: b, levels: [3, 1], kind: daily, check: manual, xp: 1}]`,
		"auto not daily": `[{slug: a, title: a, text: b, levels: [0, 1], kind: once, check: auto, rule: {type: goal_contribution}, xp: 1}]`,
		"a bad rule":     `[{slug: a, title: a, text: b, levels: [0, 1], kind: daily, check: auto, rule: {type: magic}, xp: 1}]`,
		"no count":       `[{slug: a, title: a, text: b, levels: [0, 1], kind: daily, check: auto, rule: {type: operations_logged}, xp: 1}]`,
		"a typo":         `[{slug: a, title: a, text: b, levels: [0, 1], kind: daily, check: manual, xp: 1, xpp: 2}]`,
		"twice":          `[{slug: a, title: a, text: b, levels: [0, 1], kind: daily, check: manual, xp: 1}, {slug: a, title: a, text: b, levels: [0, 1], kind: daily, check: manual, xp: 1}]`,
	} {
		if _, err := ParseQuests([]byte(data)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := ParseChallenge([]byte("slug: a\ntitle: a\ndescription: b\naccess: paid\ndays: [{title: a, task: b, check: manual, xp: 1}]")); err == nil {
		t.Error("paid access was accepted: all challenges are free or for the club")
	}
}

func TestQuestOfTheDayDoesNotRepeatForTwoWeeks(t *testing.T) {
	e := setup(t)
	q, status, err := e.s.TodayQuest(ctx, anna, today)
	if err != nil || status != StatusOffered {
		t.Fatalf("today = %+v, %s, %v", q, status, err)
	}
	again, _, _ := e.s.TodayQuest(ctx, anna, today)
	if again.Slug != q.Slug {
		t.Errorf("the quest of the day changed within the day: %s, %s", q.Slug, again.Slug)
	}
	seen := map[string]int{q.Slug: 1}
	// Four quests of the day at level 0: all of them before any comes back.
	for i := 1; i < 4; i++ {
		next, _, err := e.s.TodayQuest(ctx, anna, today.AddDate(0, 0, i))
		if err != nil {
			t.Fatal(err)
		}
		seen[next.Slug]++
	}
	if len(seen) != 4 {
		t.Errorf("quests in four days = %v", seen)
	}
}

func TestLevelDecidesTheQuests(t *testing.T) {
	e := setup(t)
	_ = e.store.RaiseLevel(ctx, anna, 4, true)
	q, _, err := e.s.TodayQuest(ctx, anna, today)
	if err != nil || q.Slug != "save-any" {
		t.Errorf("at level 4 = %+v, %v", q, err)
	}
	list, err := e.s.Quests(ctx, anna, today)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, l := range list {
		slugs = append(slugs, l.Quest.Slug)
	}
	if !slices.Contains(slugs, "market-rate") || slices.Contains(slugs, "list-debts") {
		t.Errorf("quests = %v", slugs)
	}
}

func TestAutomaticQuests(t *testing.T) {
	tests := []struct {
		name  string
		slug  string
		setup func(t *testing.T, e env)
		next  bool // judged only after the day
		want  string
	}{
		{"three operations", "log-three", func(t *testing.T, e env) {
			for range 3 {
				e.spend(t, ledger.CategoryFood, 100, "кофе", today)
			}
		}, false, StatusDone},
		{"two operations, the day goes on", "log-three", func(t *testing.T, e env) {
			e.spend(t, ledger.CategoryFood, 100, "кофе", today)
		}, false, StatusOffered},
		{"two operations, the day is over", "log-three", func(t *testing.T, e env) {
			e.spend(t, ledger.CategoryFood, 100, "кофе", today)
		}, true, StatusMissed},
		{"no delivery is judged after the day", "no-delivery", func(t *testing.T, e env) {
			e.spend(t, ledger.CategoryFood, 100, "кофе", today)
		}, false, StatusOffered},
		{"no delivery", "no-delivery", func(t *testing.T, e env) {
			e.spend(t, ledger.CategoryFood, 100, "кофе", today)
		}, true, StatusDone},
		{"a delivery", "no-delivery", func(t *testing.T, e env) {
			e.spend(t, ledger.CategoryFood, 900, "Доставка суши", today)
		}, true, StatusMissed},
		{"silence proves nothing", "no-delivery", func(*testing.T, env) {}, true, StatusMissed},
		{"a deposit", "save-any", func(t *testing.T, e env) {
			g, err := e.budgets.CreateGoal(ctx, budget.Goal{UserID: anna, Title: "Отпуск", Target: decimal.NewFromInt(1000)}, today)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.budgets.Deposit(ctx, anna, g.ID, decimal.NewFromInt(10), nil, today); err != nil {
				t.Fatal(err)
			}
		}, false, StatusDone},
		{"under the limit", "under-limit", func(t *testing.T, e env) {
			if _, err := e.budgets.SetPlan(ctx, budget.Plan{UserID: anna, Paydays: []int{5}}); err != nil {
				t.Fatal(err)
			}
			e.spend(t, ledger.CategorySalary, 90000, "", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))
			e.spend(t, ledger.CategoryFood, 100, "кофе", today)
		}, true, StatusDone},
		{"over the limit", "under-limit", func(t *testing.T, e env) {
			if _, err := e.budgets.SetPlan(ctx, budget.Plan{UserID: anna, Paydays: []int{5}}); err != nil {
				t.Fatal(err)
			}
			e.spend(t, ledger.CategorySalary, 90000, "", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))
			e.spend(t, ledger.CategoryShopping, 50000, "", today)
		}, true, StatusMissed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			period := today.Format(time.DateOnly)
			if err := e.store.OfferQuest(ctx, anna, tt.slug, period); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, e)
			day := today
			if tt.next {
				day = today.AddDate(0, 0, 1)
			}
			done, err := e.s.CheckQuests(ctx, always(day), at)
			if err != nil {
				t.Fatal(err)
			}
			rows, _ := e.store.UserQuests(ctx, anna)
			if len(rows) != 1 || rows[0].Status != tt.want {
				t.Fatalf("rows = %+v, want %s", rows, tt.want)
			}
			if (len(done) == 1) != (tt.want == StatusDone) {
				t.Errorf("done = %+v", done)
			}
			again, _ := e.s.CheckQuests(ctx, always(day), at)
			if len(again) != 0 {
				t.Errorf("counted twice: %+v", again)
			}
			p, _ := e.s.Profile(ctx, anna)
			if tt.want == StatusDone && p.XP == 0 || tt.want != StatusDone && p.XP != 0 {
				t.Errorf("xp = %d", p.XP)
			}
		})
	}
}

func TestStreak(t *testing.T) {
	e := setup(t)
	d := func(n int) time.Time { return today.AddDate(0, 0, n) }
	for i := range 6 {
		if m, err := e.s.Touch(ctx, anna, d(i)); err != nil || m != nil {
			t.Fatalf("day %d: %+v, %v", i, m, err)
		}
	}
	if m, _ := e.s.Touch(ctx, anna, d(5)); m != nil {
		t.Error("the same day counted again")
	}
	m, err := e.s.Touch(ctx, anna, d(6))
	if err != nil || m == nil || m.Days != 7 || m.XP != 20 {
		t.Fatalf("day 7 = %+v, %v", m, err)
	}
	p, _ := e.s.Profile(ctx, anna)
	if p.CurrentStreak(d(6)) != 7 || p.CurrentStreak(d(7)) != 7 || p.CurrentStreak(d(8)) != 0 || p.XP != 20 {
		t.Errorf("profile = %+v", p)
	}
	if _, err := e.s.Touch(ctx, anna, d(9)); err != nil {
		t.Fatal(err)
	}
	p, _ = e.s.Profile(ctx, anna)
	if p.Streak != 1 || p.BestStreak != 7 {
		t.Errorf("a missed day starts over: %+v", p)
	}
}

func TestLevelsGoUpByTheRules(t *testing.T) {
	check := func(t *testing.T, e env, want int, up bool) {
		t.Helper()
		level, raised, err := e.s.CheckLevel(ctx, anna, today)
		if err != nil || level != want || raised != up {
			t.Errorf("CheckLevel = %d, %v, %v, want %d, %v", level, raised, err, want, up)
		}
	}
	t.Run("records on 10 days of 14", func(t *testing.T) {
		e := setup(t)
		for i := range 9 {
			e.spend(t, ledger.CategoryFood, 100, "", today.AddDate(0, 0, -i))
		}
		check(t, e, 0, false)
		e.spend(t, ledger.CategoryFood, 100, "", today.AddDate(0, 0, -9))
		check(t, e, 1, true)
		check(t, e, 1, false) // no cushion yet
	})
	t.Run("a cushion of three months", func(t *testing.T) {
		e := setup(t)
		_ = e.store.RaiseLevel(ctx, anna, 1, true)
		for back := 1; back <= 3; back++ {
			e.spend(t, ledger.CategoryFood, 30000, "", MonthStart(today, back))
		}
		g, err := e.budgets.CreateGoal(ctx, budget.Goal{UserID: anna, Title: "Подушка безопасности", Target: decimal.NewFromInt(500000)}, today)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.budgets.Deposit(ctx, anna, g.ID, decimal.NewFromInt(89000), nil, today); err != nil {
			t.Fatal(err)
		}
		check(t, e, 1, false)
		if _, err := e.budgets.Deposit(ctx, anna, g.ID, decimal.NewFromInt(1000), nil, today); err != nil {
			t.Fatal(err)
		}
		check(t, e, 2, true)
	})
	t.Run("another goal is not a cushion", func(t *testing.T) {
		e := setup(t)
		_ = e.store.RaiseLevel(ctx, anna, 1, true)
		g, _ := e.budgets.CreateGoal(ctx, budget.Goal{UserID: anna, Title: "Отпуск", Target: decimal.NewFromInt(500000)}, today)
		_, _ = e.budgets.Deposit(ctx, anna, g.ID, decimal.NewFromInt(400000), nil, today)
		e.spend(t, ledger.CategoryFood, 100, "", MonthStart(today, 1))
		check(t, e, 1, false)
	})
	t.Run("level 3 is the person's word", func(t *testing.T) {
		e := setup(t)
		_ = e.store.RaiseLevel(ctx, anna, 2, true)
		check(t, e, 2, false)
		if ok, _ := e.s.Confirm(ctx, anna, 5); ok {
			t.Error("confirmed a level two steps up")
		}
		if ok, _ := e.s.Confirm(ctx, anna, 3); !ok {
			t.Error("level 3 was not confirmed")
		}
		if ok, _ := e.s.Confirm(ctx, anna, 4); ok {
			t.Error("level 4 is decided by the data")
		}
	})
	t.Run("the level never goes down", func(t *testing.T) {
		e := setup(t)
		_ = e.store.RaiseLevel(ctx, anna, 3, true)
		p, err := e.s.ApplyTest(ctx, anna, 1)
		if err != nil || p.Level != 3 {
			t.Errorf("after a lower test = %+v, %v", p, err)
		}
	})
}

func TestChallenge(t *testing.T) {
	e := setup(t)
	const slug = "no-spend-week"
	c, joined, err := e.s.Join(ctx, slug, anna, today)
	if err != nil || !joined {
		t.Fatalf("join = %v, %v", joined, err)
	}
	day := func(n int) time.Time { return today.AddDate(0, 0, n-1) }

	// Day 1 is automatic: three records.
	for range 3 {
		e.spend(t, ledger.CategoryFood, 100, "", day(1))
	}
	done, finished, err := e.s.CheckChallenges(ctx, always(day(1)))
	if err != nil || len(done) != 1 || len(finished) != 0 {
		t.Fatalf("day 1 = %+v, %+v, %v", done, finished, err)
	}
	again, _, _ := e.s.CheckChallenges(ctx, always(day(1)))
	if len(again) != 0 {
		t.Errorf("counted twice: %+v", again)
	}

	// Day 2 is by hand, postponed and then done.
	if _, err := e.s.MarkDay(ctx, slug, anna, 2, DayLater, day(2)); err != nil {
		t.Fatal(err)
	}
	later, _ := e.s.LaterMarks(ctx, day(2))
	if len(later) != 1 {
		t.Errorf("later = %+v", later)
	}
	d, err := e.s.MarkDay(ctx, slug, anna, 2, DayDone, day(2))
	if err != nil || !d.Awarded {
		t.Fatalf("done = %+v, %v", d, err)
	}
	if _, err := e.s.MarkDay(ctx, slug, anna, 2, DaySkipped, day(2)); err != nil {
		t.Fatal(err)
	}
	marks, _ := e.store.DayMarks(ctx, slug, anna)
	if marks[1].Status != DayDone {
		t.Errorf("a done day was undone: %+v", marks)
	}
	if _, err := e.s.MarkDay(ctx, slug, anna, 3, DayDone, day(2)); err == nil {
		t.Error("marked a day that has not come")
	}

	_, finished, err = e.s.CheckChallenges(ctx, always(day(len(c.Days))))
	if err != nil || len(finished) != 0 {
		t.Fatalf("the last day is not over: %+v, %v", finished, err)
	}
	_, finished, err = e.s.CheckChallenges(ctx, always(day(len(c.Days)+1)))
	if err != nil || len(finished) != 1 || finished[0].DoneDays != 2 || finished[0].XP != c.XPFinish {
		t.Fatalf("finished = %+v, %v", finished, err)
	}
	if _, finished, _ = e.s.CheckChallenges(ctx, always(day(len(c.Days)+2))); len(finished) != 0 {
		t.Error("finished twice")
	}
	if active, _ := e.s.Active(ctx, anna, day(8)); len(active) != 0 {
		t.Errorf("still active: %+v", active)
	}
}

func TestChallengeWithACommonStart(t *testing.T) {
	data := `
slug: stream
title: Поток
description: Все вместе
access: free
starts_on: "2026-11-20"
days: [{title: a, task: b, check: manual, xp: 1}]
`
	c, err := ParseChallenge([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if !c.StartFor(today).Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %s", c.StartFor(today))
	}
	if !c.Open(today) || !c.Open(time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)) {
		t.Error("open before the start and for three days after it")
	}
	if c.Open(time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)) {
		t.Error("open too late")
	}
}

type clubMembers map[int64]bool

func (c clubMembers) Has(_ context.Context, userID int64, _ string) bool { return c[userID] }

func TestClubChallengeNeedsTheClub(t *testing.T) {
	e := setup(t)
	if _, _, err := e.s.Join(ctx, "club-debt-free", anna, today); !errors.Is(err, ErrClubOnly) {
		t.Fatalf("without the club: %v", err)
	}
	e.s.Access = clubMembers{anna: true}
	if _, joined, err := e.s.Join(ctx, "club-debt-free", anna, today); err != nil || !joined {
		t.Errorf("in the club: %v, %v", joined, err)
	}
}

func TestStreakFreezeCoversOneDayAWeek(t *testing.T) {
	e := setup(t)
	d := func(n int) time.Time { return today.AddDate(0, 0, n) }
	touch := func(n int) int {
		t.Helper()
		if _, err := e.s.Touch(ctx, anna, d(n)); err != nil {
			t.Fatal(err)
		}
		p, _ := e.s.Profile(ctx, anna)
		return p.Streak
	}
	touch(0)
	if got := touch(2); got != 1 {
		t.Fatalf("without the club a missed day starts over: %d", got)
	}
	e.s.Access = clubMembers{anna: true}
	touch(3)
	if got := touch(5); got != 4 { // days 2, 3, the frozen 4 and 5
		t.Fatalf("frozen = %d", got)
	}
	if got := touch(7); got != 1 { // the same week: no second freeze
		t.Errorf("a second freeze in a week = %d", got)
	}
}

func TestYesterdayShareNamesNobody(t *testing.T) {
	e := setup(t)
	const slug = "no-spend-week"
	if _, _, err := e.s.Join(ctx, slug, anna, today); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.s.YesterdayShare(ctx, slug, today.AddDate(0, 0, 1)); ok {
		t.Error("a share of one person gives them away")
	}
	for id := int64(2); id <= 4; id++ {
		if _, _, err := e.s.Join(ctx, slug, id, today); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.s.MarkDay(ctx, slug, anna, 1, DayDone, today); err != nil {
		t.Fatal(err)
	}
	if share, ok, _ := e.s.YesterdayShare(ctx, slug, today.AddDate(0, 0, 1)); !ok || share != 25 {
		t.Errorf("share = %d, %v", share, ok)
	}
}

func TestReferrals(t *testing.T) {
	e := setup(t)
	code, err := e.s.ReferralCode(ctx, anna)
	if err != nil || code == "" {
		t.Fatalf("code = %q, %v", code, err)
	}
	if again, _ := e.s.ReferralCode(ctx, anna); again != code {
		t.Error("the code changed")
	}
	if err := e.s.Refer(ctx, code, anna); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := e.s.RewardReferrer(ctx, anna); id != 0 {
		t.Error("paid for one's own link")
	}
	if err := e.s.Refer(ctx, code, 2); err != nil {
		t.Fatal(err)
	}
	if id, friends, _ := e.s.RewardReferrer(ctx, 2); id != anna || friends != 1 {
		t.Errorf("referrer = %d, friends = %d", id, friends)
	}
	if id, _, _ := e.s.RewardReferrer(ctx, 2); id != 0 {
		t.Error("paid twice")
	}
	if p, _ := e.s.Profile(ctx, anna); p.XP != ReferralXP {
		t.Errorf("xp = %d", p.XP)
	}
}
