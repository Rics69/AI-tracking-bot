// Package events records what people do, for the owner's /stats. A failure to
// record never touches the reply to the user: writes happen in the background.
package events

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Names of the events.
const (
	Start          = "start"
	Consented      = "consented"
	ExpenseAdded   = "expense_added"
	IncomeAdded    = "income_added"
	AILimitHit     = "ai_limit_hit"
	BotBlocked     = "bot_blocked"
	MorningSent    = "morning_sent"
	MorningClicked = "morning_clicked"
	MiniAppOpen    = "miniapp_open"

	OnboardingStep  = "onboarding_step"
	OnboardingDone  = "onboarding_done"
	DreamCalculated = "dream_calculated"
	GoalCreated     = "goal_created"
	BudgetSet       = "budget_set"

	QuestDone        = "quest_done"
	LevelUp          = "level_up"
	ChallengeJoined  = "challenge_joined"
	ChallengeDayDone = "challenge_day_done"
	CardGenerated    = "card_generated"
	ReferralJoined   = "referral_joined"
	ReviewGiven      = "review_given"
)

// Ways an operation gets recorded, the "via" property.
const (
	ViaFast    = "fast"
	ViaAI      = "ai"
	ViaMiniApp = "miniapp"
)

// Props are the details of an event.
type Props map[string]any

const writeTimeout = 5 * time.Second

// Store persists events. userID 0 means an event of nobody in particular.
type Store interface {
	Add(ctx context.Context, userID int64, name string, props Props) error
}

// Tracker writes events in the background. A nil Tracker records nothing, so
// parts that do not care about analytics need no setup.
type Tracker struct {
	store Store
	log   *slog.Logger
	wg    sync.WaitGroup
}

func NewTracker(store Store, log *slog.Logger) *Tracker {
	return &Tracker{store: store, log: log}
}

// Track records an event without waiting for the database.
func (t *Tracker) Track(ctx context.Context, userID int64, name string, props Props) {
	if t == nil {
		return
	}
	t.wg.Go(func() {
		// The request may be over by the time this runs.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
		defer cancel()
		if err := t.store.Add(ctx, userID, name, props); err != nil {
			t.log.WarnContext(ctx, "record event", "name", name, "err", err)
		}
	})
}

// Wait returns when everything tracked so far is written: for shutdown and tests.
func (t *Tracker) Wait() {
	if t != nil {
		t.wg.Wait()
	}
}
