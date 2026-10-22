// Package savik decides the mood of Savik, the mascot, from the user's money.
// The state is computed by code, so it is exact and costs no tokens; how each
// state looks and what Savik says lives in the mini app.
package savik

import "github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"

// State is Savik's mood.
type State string

const (
	// Neutral: nothing alarming, or too little data to say.
	Neutral State = "neutral"
	// Empty: this month's expenses are above the incomes the user recorded.
	Empty State = "empty"
)

// Of returns the state for the user's operations of the current month.
// Incomes are optional, so a user who records only expenses is not shown as
// broke: without incomes there is nothing to compare with.
func Of(month ledger.Summary) State {
	if month.Income.Count > 0 && month.Balance().IsNegative() {
		return Empty
	}
	return Neutral
}
