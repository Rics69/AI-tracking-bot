package report

import (
	"strconv"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/card"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
)

// Cards for stories. By default they show percents and levels, never money:
// amounts appear only after the user asks for them with a button.

// GoalCard is the card of a goal.
func GoalCard(t texts.Bot, g budget.Goal, amounts bool) card.Card {
	c := card.Card{Pose: card.PoseSaving, Eyebrow: t.CardGoal, Value: strconv.Itoa(percent(g)) + "%", Title: g.Title}
	if percent(g) >= 100 {
		c.Pose, c.Glow = card.PoseFull, card.GlowOrange
	}
	if amounts {
		c.Lines = []string{texts.Fill(t.CardAmounts, "saved", Money(g.Saved), "target", Money(g.Target))}
	}
	return c
}

// LevelCard is the card of a new level.
func LevelCard(t texts.Bot, level int) card.Card {
	return card.Card{Pose: card.PoseProud, Glow: card.GlowOrange, Eyebrow: t.CardLevel,
		Value: texts.Fill(t.CardLevelValue, "level", strconv.Itoa(level)), Title: LevelName(t, level)}
}

// StreakCard is the card of a streak mark.
func StreakCard(t texts.Bot, days int) card.Card {
	return card.Card{Pose: card.PoseJoy, Glow: card.GlowOrange, Eyebrow: t.CardStreak, Value: Days(days)}
}

// ChallengeCard is the certificate of a finished challenge.
func ChallengeCard(t texts.Bot, title string, done, total int) card.Card {
	return card.Card{Pose: card.PoseProud, Eyebrow: t.CardChallenge, Value: strconv.Itoa(done*100/max(total, 1)) + "%", Title: title,
		Lines: []string{texts.Fill(t.CardChallengeDays, "done", strconv.Itoa(done), "total", strconv.Itoa(total))}}
}

// LevelName is the name of a step of the ladder.
func LevelName(t texts.Bot, level int) string {
	if level < 0 || level >= len(t.LevelNames) {
		return ""
	}
	return t.LevelNames[level]
}

// Days is "13 дней".
func Days(n int) string { return strconv.Itoa(n) + " " + plural(n, "день", "дня", "дней") }
