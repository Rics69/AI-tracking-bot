package report

import (
	"context"
	"log/slog"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
)

const (
	kindClubWarn    = "club_warn"    // the period is the date the club ends
	kindClubRemoved = "club_removed" // the same

	clubWarnBefore = 3 * 24 * time.Hour
	clubGrace      = 3 * 24 * time.Hour
	// clubLookBack is how far back an ending can still be acted on.
	clubLookBack = 30 * 24 * time.Hour
)

// ClubEndings tells when access runs out; *access.Service implements it.
type ClubEndings interface {
	Endings(ctx context.Context, from time.Time) ([]access.Ending, error)
}

// ClubSync keeps the club's chats in line with entitlements: it warns three
// days before the end, and removes people three days after it. Entitlements
// are the source of truth, not the list of chat members.
type ClubSync struct {
	access ClubEndings
	runs   Runs
	send   SendFunc
	remove func(ctx context.Context, userID int64) error
	t      texts.Bot
	now    func() time.Time
	log    *slog.Logger
}

func NewClubSync(a ClubEndings, runs Runs, send SendFunc, remove func(ctx context.Context, userID int64) error, t texts.Bot, log *slog.Logger) *ClubSync {
	return &ClubSync{access: a, runs: runs, send: send, remove: remove, t: t, now: time.Now, log: log}
}

// Tick warns and removes whoever is due.
func (c *ClubSync) Tick(ctx context.Context) {
	now := c.now()
	endings, err := c.access.Endings(ctx, now.Add(-clubLookBack))
	if err != nil {
		c.log.ErrorContext(ctx, "club sync", "err", err)
		return
	}
	for _, e := range endings {
		period := e.At.Format(time.DateOnly)
		switch {
		case e.At.After(now) && e.At.Sub(now) <= clubWarnBefore:
			c.once(ctx, kindClubWarn, period, e.UserID, func() error { return c.send(ctx, e.UserID, c.t.ClubEnding) })
		case now.Sub(e.At) >= clubGrace:
			c.once(ctx, kindClubRemoved, period, e.UserID, func() error { return c.remove(ctx, e.UserID) })
		}
	}
}

func (c *ClubSync) once(ctx context.Context, kind, period string, userID int64, do func() error) {
	claimed, err := c.runs.Claim(ctx, kind, period, userID)
	if err != nil || !claimed {
		return
	}
	if err := do(); err != nil {
		c.log.WarnContext(ctx, "club sync", "kind", kind, "user_id", userID, "err", err)
		if relErr := c.runs.Release(ctx, kind, period, userID); relErr != nil {
			c.log.ErrorContext(ctx, "club sync: release claim", "err", relErr)
		}
	}
}
