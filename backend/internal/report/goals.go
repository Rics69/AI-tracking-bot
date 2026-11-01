package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
)

// kindGoalProgress is the Runs kind of goal milestones; the period is
// "<goal id>:<level>", so taking money back and saving it again stays silent.
const kindGoalProgress = "goal_progress"

// goalLevels are the milestones, highest first.
var goalLevels = []int{100, 75, 50, 25}

// AllGoals lists the goals of every user; *budget.Service implements it.
type AllGoals interface {
	AllGoals(ctx context.Context) ([]budget.Goal, error)
}

// GoalProgress tells users about milestones of their goals. It watches the
// database, not the deposits: money is also put aside in the mini app, and
// only the bot can write to Telegram.
type GoalProgress struct {
	goals   AllGoals
	members Members
	runs    Runs
	send    SendFunc
	// SendCard, if set, sends a milestone as a card; the text stays as its caption.
	SendCard CardSend
	t        texts.Bot
	log      *slog.Logger
}

func NewGoalProgress(goals AllGoals, members Members, runs Runs, send SendFunc, t texts.Bot, log *slog.Logger) *GoalProgress {
	return &GoalProgress{goals: goals, members: members, runs: runs, send: send, t: t, log: log}
}

// Run checks the goals every few minutes until ctx is cancelled.
func (p *GoalProgress) Run(ctx context.Context) {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		p.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick sends the milestones reached since the last one.
func (p *GoalProgress) Tick(ctx context.Context) {
	goals, err := p.goals.AllGoals(ctx)
	if err != nil {
		p.log.ErrorContext(ctx, "goal progress: list goals", "err", err)
		return
	}
	if len(goals) == 0 {
		return
	}
	members, err := p.members.All(ctx)
	if err != nil {
		p.log.ErrorContext(ctx, "goal progress: list users", "err", err)
		return
	}
	wanted := make(map[int64]bool, len(members))
	for _, m := range members {
		wanted[m.TelegramID] = m.Consented() && !m.Blocked() && m.GoalUpdates
	}
	for _, g := range goals {
		if !wanted[g.UserID] {
			continue
		}
		if err := p.check(ctx, g); err != nil {
			p.log.ErrorContext(ctx, "goal progress", "goal_id", g.ID, "err", err)
		}
	}
}

// check sends the highest level passed; the ones below it are marked as told:
// a goal that jumps from 10 to 80% gets one message, not three.
func (p *GoalProgress) check(ctx context.Context, g budget.Goal) error {
	share, told := percent(g), false
	for _, level := range goalLevels {
		if share < level {
			continue
		}
		period := fmt.Sprintf("%d:%d", g.ID, level)
		claimed, err := p.runs.Claim(ctx, kindGoalProgress, period, g.UserID)
		if err != nil {
			return err
		}
		if !claimed || told {
			told = true
			continue
		}
		told = true
		err = p.deliver(ctx, g)
		if errors.Is(err, ErrUndeliverable) {
			p.log.WarnContext(ctx, "goal progress undeliverable", "user_id", g.UserID, "err", err)
			continue
		}
		if err != nil {
			if relErr := p.runs.Release(ctx, kindGoalProgress, period, g.UserID); relErr != nil {
				p.log.ErrorContext(ctx, "goal progress: release claim", "err", relErr)
			}
			return err
		}
	}
	return nil
}

func (p *GoalProgress) deliver(ctx context.Context, g budget.Goal) error {
	if p.SendCard == nil {
		return p.send(ctx, g.UserID, p.message(g))
	}
	rows := [][]game.Action{{{Label: p.t.CardShowAmounts, Data: game.ActGoalCard + ":" + strconv.FormatInt(g.ID, 10)}}}
	return p.SendCard(ctx, g.UserID, GoalCard(p.t, g, false), p.caption(g), rows)
}

// caption is the message without money: a card may be forwarded as it is.
func (p *GoalProgress) caption(g budget.Goal) string {
	if percent(g) >= 100 {
		return texts.Fill(p.t.GoalReachedCaption, "title", g.Title)
	}
	return texts.Fill(p.t.GoalProgressCaption, "title", g.Title, "percent", strconv.Itoa(percent(g)))
}

func (p *GoalProgress) message(g budget.Goal) string {
	if percent(g) >= 100 {
		return goalLine(p.t.GoalReached, g)
	}
	return goalLine(p.t.GoalProgress, g)
}
