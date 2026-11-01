// Package flow keeps the dialog a user is in the middle of: one at a time,
// with its step and answers, so a dialog survives a restart and an interruption.
// What the steps say and do lives in the telegram package.
package flow

import (
	"context"
	"errors"
	"time"
)

// TTL is how long a dialog waits. A week later "кофе 350" must be an expense,
// not a wrong answer to a question long forgotten.
const TTL = 24 * time.Hour

// ErrNone: the user is not in a dialog.
var ErrNone = errors.New("no active flow")

// State is a dialog in progress. Values are strings: amounts stay exact in JSON.
type State struct {
	UserID    int64
	Flow      string
	Step      string
	Data      map[string]string
	UpdatedAt time.Time
}

// Expired reports a dialog left alone for longer than TTL.
func (s State) Expired(now time.Time) bool {
	return now.Sub(s.UpdatedAt) > TTL
}

// Store persists dialogs.
type Store interface {
	Get(ctx context.Context, userID int64) (State, error) // ErrNone: no dialog
	Save(ctx context.Context, s State) error
	Delete(ctx context.Context, userID int64) error
}
