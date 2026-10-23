// Package savik decides the mood of Savik, the mascot, from the user's money.
// The state is computed by code, so it is exact and costs no tokens; how each
// state looks and what Savik says lives in the mini app and in texts.yaml.
package savik

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// State is Savik's mood.
type State string

const (
	// Neutral: nothing alarming, or too little data to say.
	Neutral State = "neutral"
	// Full: a goal was reached in the last 7 days.
	Full State = "full"
	// Empty: nothing is left until the next payday; without a plan, this
	// month's expenses are above the recorded incomes.
	Empty State = "empty"
	// Hungry: today's expenses are above the daily limit.
	Hungry State = "hungry"
	// Fed: a goal is at least 80% saved.
	Fed State = "fed"
)

// fullDays is how long Savik celebrates a reached goal.
const fullDays = 7

var fedShare = decimal.RequireFromString("0.8")

// Input is the user's money as of today. Budget is nil without a plan.
type Input struct {
	Today  time.Time
	Month  ledger.Summary // the user's own operations from the 1st to today
	Budget *budget.Budget
	Goals  []budget.Goal
}

// Of returns Savik's state; the first matching rule wins.
func Of(in Input) State {
	for _, g := range in.Goals {
		if g.AchievedOn != nil && in.Today.Sub(*g.AchievedOn) < fullDays*24*time.Hour {
			return Full
		}
	}
	switch b := in.Budget; {
	case b != nil && b.Ready && !b.Remaining().IsPositive():
		return Empty
	case b != nil && b.Overspent():
		return Hungry
	// Incomes are optional, so a user who records only expenses is not shown
	// as broke: without incomes there is nothing to compare with.
	case (b == nil || !b.Ready) && in.Month.Income.Count > 0 && in.Month.Balance().IsNegative():
		return Empty
	}
	for _, g := range in.Goals {
		if g.Share().GreaterThanOrEqual(fedShare) {
			return Fed
		}
	}
	return Neutral
}
