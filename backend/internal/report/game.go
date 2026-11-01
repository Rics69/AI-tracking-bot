package report

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/card"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const (
	kindStreakNudge = "streak_nudge" // the period is the user's date
	kindEvening     = "evening_task" // the period is "<slug>:<day>"

	eveningHour     = 20 // postponed tasks come back
	streakNudgeHour = 21
)

// ActionSend delivers a message with buttons.
type ActionSend func(ctx context.Context, chatID int64, text string, rows [][]game.Action) error

// CardSend delivers a card with a caption and buttons.
type CardSend func(ctx context.Context, chatID int64, c card.Card, caption string, rows [][]game.Action) error

// Game watches the data and tells users what they have earned. It is a loop
// over the database for the same reason as GoalProgress: operations and
// deposits also come through the API, and only the bot can write to Telegram.
type Game struct {
	game    *game.Service
	members Members
	runs    Runs
	say     ActionSend
	card    CardSend
	t       texts.Bot
	loc     *time.Location
	now     func() time.Time
	log     *slog.Logger

	mu      sync.Mutex
	checked map[int64]time.Time // the day the level of a user was last checked
}

func NewGame(g *game.Service, members Members, runs Runs, say ActionSend, cards CardSend, t texts.Bot, loc *time.Location, log *slog.Logger) *Game {
	return &Game{game: g, members: members, runs: runs, say: say, card: cards, t: t, loc: loc,
		now: time.Now, log: log, checked: map[int64]time.Time{}}
}

// Run checks every few minutes until ctx is cancelled.
func (g *Game) Run(ctx context.Context) {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		g.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick does everything that has come due. One failure does not stop the rest.
func (g *Game) Tick(ctx context.Context) {
	members, err := g.members.All(ctx)
	if err != nil {
		g.log.ErrorContext(ctx, "game: list users", "err", err)
		return
	}
	now := g.now()
	people := make(map[int64]user.User, len(members))
	for _, m := range members {
		if m.Consented() && !m.Blocked() {
			people[m.TelegramID] = m
		}
	}
	today := func(id int64) (time.Time, bool) {
		m, ok := people[id]
		if !ok {
			return time.Time{}, false
		}
		return dateOfTime(now.In(m.Location(g.loc))), true
	}
	g.streaks(ctx, people, now)
	g.quests(ctx, today, now)
	g.challenges(ctx, today)
	for _, m := range people {
		local := now.In(m.Location(g.loc))
		g.level(ctx, m, dateOfTime(local))
		if local.Hour() >= streakNudgeHour {
			g.nudge(ctx, m, dateOfTime(local))
		}
		if local.Hour() >= eveningHour {
			g.evening(ctx, m, dateOfTime(local))
		}
	}
}

func (g *Game) streaks(ctx context.Context, people map[int64]user.User, now time.Time) {
	active, err := g.game.ActiveSince(ctx, now.Add(-48*time.Hour))
	if err != nil {
		g.log.ErrorContext(ctx, "game: active users", "err", err)
		return
	}
	for id, last := range active {
		m, ok := people[id]
		if !ok {
			continue
		}
		// The day of the record, not today: a record at 23:58 counts for its
		// day even if the next check comes after midnight.
		mark, err := g.game.Touch(ctx, id, dateOfTime(last.In(m.Location(g.loc))))
		if err != nil {
			g.log.ErrorContext(ctx, "game: streak", "user_id", id, "err", err)
			continue
		}
		if mark != nil {
			g.sendCard(ctx, id, StreakCard(g.t, mark.Days),
				texts.Fill(g.t.StreakMark, "days", Days(mark.Days), "xp", strconv.Itoa(mark.XP)), nil)
		}
	}
}

func (g *Game) quests(ctx context.Context, today func(int64) (time.Time, bool), now time.Time) {
	done, err := g.game.CheckQuests(ctx, today, now)
	if err != nil {
		g.log.ErrorContext(ctx, "game: quests", "err", err)
	}
	for _, d := range done {
		g.tell(ctx, d.UserID, texts.Fill(g.t.QuestDone, "title", d.Title, "xp", strconv.Itoa(d.XP)))
	}
}

func (g *Game) challenges(ctx context.Context, today func(int64) (time.Time, bool)) {
	done, finished, err := g.game.CheckChallenges(ctx, today)
	if err != nil {
		g.log.ErrorContext(ctx, "game: challenges", "err", err)
	}
	for _, d := range done {
		g.tell(ctx, d.UserID, texts.Fill(g.t.QuestDone, "title", d.Title, "xp", strconv.Itoa(d.XP)))
	}
	for _, f := range finished {
		total := len(f.Challenge.Days)
		caption := texts.Fill(g.t.ChallengeFinished, "title", f.Challenge.Title, "done", strconv.Itoa(f.DoneDays),
			"total", strconv.Itoa(total), "saved", Money(f.Saved), "xp", strconv.Itoa(f.XP))
		if f.Counted > 0 {
			caption += "\n" + texts.Fill(g.t.ChallengeFinishedLimit, "under", strconv.Itoa(f.Under), "counted", strconv.Itoa(f.Counted))
		}
		g.sendCard(ctx, f.UserID, ChallengeCard(g.t, f.Challenge.Title, f.DoneDays, total), caption, nil)
	}
}

// level checks once a day whether the data has reached the next level.
func (g *Game) level(ctx context.Context, m user.User, day time.Time) {
	g.mu.Lock()
	seen := g.checked[m.TelegramID].Equal(day)
	g.checked[m.TelegramID] = day
	g.mu.Unlock()
	if seen {
		return
	}
	level, up, err := g.game.CheckLevel(ctx, m.TelegramID, day)
	if err != nil {
		g.log.ErrorContext(ctx, "game: level", "user_id", m.TelegramID, "err", err)
		return
	}
	if up {
		g.sendCard(ctx, m.TelegramID, LevelCard(g.t, level),
			texts.Fill(g.t.LevelUp, "level", strconv.Itoa(level), "name", LevelName(g.t, level)), nil)
	}
}

// nudge saves a streak that is about to break. Only those who have one get
// it: an evening message to everyone would be noise.
func (g *Game) nudge(ctx context.Context, m user.User, day time.Time) {
	if !m.StreakReminder {
		return
	}
	p, err := g.game.Profile(ctx, m.TelegramID)
	if err != nil {
		g.log.ErrorContext(ctx, "game: profile", "user_id", m.TelegramID, "err", err)
		return
	}
	if p.LastDay == nil || !p.LastDay.Equal(day.AddDate(0, 0, -1)) {
		return // today counts already, or there is nothing to save
	}
	period := day.Format(time.DateOnly)
	claimed, err := g.runs.Claim(ctx, kindStreakNudge, period, m.TelegramID)
	if err != nil || !claimed {
		return
	}
	rows := [][]game.Action{{{Label: g.t.NoSpendButton, Data: game.ActNoSpend}}}
	err = g.say(ctx, m.TelegramID, texts.Fill(g.t.StreakNudge, "days", Days(p.Streak)), rows)
	g.finishClaim(ctx, kindStreakNudge, period, m.TelegramID, err)
}

// evening brings back the tasks postponed in the morning.
func (g *Game) evening(ctx context.Context, m user.User, day time.Time) {
	active, err := g.game.Active(ctx, m.TelegramID, day)
	if err != nil {
		g.log.ErrorContext(ctx, "game: challenges", "user_id", m.TelegramID, "err", err)
		return
	}
	for _, cur := range active {
		if cur.Mark != game.DayLater || cur.Today == nil {
			continue
		}
		period := cur.Challenge.Slug + ":" + day.Format(time.DateOnly)
		claimed, err := g.runs.Claim(ctx, kindEvening, period, m.TelegramID)
		if err != nil || !claimed {
			continue
		}
		err = g.say(ctx, m.TelegramID, texts.Fill(g.t.ChallengeEvening, "task", cur.Today.Task), challengeRows(g.t, cur, false))
		g.finishClaim(ctx, kindEvening, period, m.TelegramID, err)
	}
}

// finishClaim gives a claim back if the message may yet get through.
func (g *Game) finishClaim(ctx context.Context, kind, period string, userID int64, err error) {
	if err == nil || errors.Is(err, ErrUndeliverable) {
		return
	}
	g.log.ErrorContext(ctx, "game: send", "kind", kind, "user_id", userID, "err", err)
	if relErr := g.runs.Release(ctx, kind, period, userID); relErr != nil {
		g.log.ErrorContext(ctx, "game: release claim", "err", relErr)
	}
}

func (g *Game) tell(ctx context.Context, userID int64, text string) {
	if err := g.say(ctx, userID, text, nil); err != nil {
		g.log.WarnContext(ctx, "game: send", "user_id", userID, "err", err)
	}
}

func (g *Game) sendCard(ctx context.Context, userID int64, c card.Card, caption string, rows [][]game.Action) {
	if err := g.card(ctx, userID, c, caption, rows); err != nil {
		g.log.WarnContext(ctx, "game: send card", "user_id", userID, "err", err)
	}
}

// challengeRows are the buttons of a task.
func challengeRows(t texts.Bot, cur game.Current, later bool) [][]game.Action {
	data := game.ActDay + ":" + cur.Challenge.Slug + ":" + strconv.Itoa(cur.DayNo) + ":"
	row := []game.Action{{Label: t.ChallengeDoneButton, Data: data + game.DayDone}}
	if later {
		row = append(row, game.Action{Label: t.ChallengeLaterButton, Data: data + game.DayLater})
	}
	return [][]game.Action{append(row, game.Action{Label: t.ChallengeSkipButton, Data: data + game.DaySkipped})}
}

// MorningExtra is what the game adds to the morning message. Alone means it
// is worth a message by itself: a task of a challenge is, a quest is not.
type MorningExtra struct {
	Lines []string
	Rows  [][]game.Action
	Alone bool
}

// GameMorning fills the morning message with the quest of the day and tasks of
// challenges, so there is one message in the morning, not three.
type GameMorning struct {
	Game  *game.Service
	Texts texts.Bot
}

// Reminded lists those who get a morning message because of the game.
func (g GameMorning) Reminded(ctx context.Context) ([]int64, error) {
	return g.Game.ChallengeUsers(ctx)
}

// Morning builds the lines of the day.
func (g GameMorning) Morning(ctx context.Context, m user.User, today time.Time) (MorningExtra, error) {
	var out MorningExtra
	t := g.Texts
	active, err := g.Game.Active(ctx, m.TelegramID, today)
	if err != nil {
		return out, err
	}
	for _, cur := range active {
		if cur.Today == nil || cur.Mark != "" {
			continue
		}
		out.Alone = true
		out.Lines = append(out.Lines, texts.Fill(t.MorningChallenge, "title", cur.Challenge.Title,
			"day", strconv.Itoa(cur.DayNo), "total", strconv.Itoa(len(cur.Challenge.Days)), "task", cur.Today.Task))
		share, ok, err := g.Game.YesterdayShare(ctx, cur.Challenge.Slug, today)
		if err != nil {
			return out, err
		}
		if ok {
			out.Lines = append(out.Lines, texts.Fill(t.MorningShare, "percent", strconv.Itoa(share)))
		}
		out.Rows = append(out.Rows, challengeRows(t, cur, true)...)
	}
	q, status, err := g.Game.TodayQuest(ctx, m.TelegramID, today)
	switch {
	case errors.Is(err, game.ErrUnknown):
	case err != nil:
		return out, err
	case status == game.StatusOffered:
		out.Lines = append(out.Lines, texts.Fill(t.MorningQuest, "title", q.Title, "xp", strconv.Itoa(q.XP)))
		if q.Check == game.CheckManual {
			out.Rows = append(out.Rows, []game.Action{{Label: t.QuestDoneButton, Data: game.ActQuest + ":" + q.Slug}})
		}
	}
	return out, nil
}
