// Package user describes the people who talk to the bot: Telegram users,
// each a member of exactly one family.
package user

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound means there is no user with the given Telegram ID.
var ErrNotFound = errors.New("user not found")

// User is a person identified by their Telegram ID.
type User struct {
	TelegramID int64
	// FamilyID is the family the user belongs to. Someone who has not joined
	// anyone is the only member and the head of a family of their own.
	FamilyID  int64
	FirstName string
	Username  string // may be empty: not every Telegram account has one
	IsHead    bool   // the head sees the expenses of the whole family
	// WeeklyReport: the bot sends the weekly summary (on by default).
	WeeklyReport bool
	Timezone     string     // IANA name; empty means the app default
	ConsentedAt  *time.Time // accepted the data policy; nil: the bot asks first
	BlockedAt    *time.Time // banned: the bot does not serve the user
	LastSeenAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Consented reports whether the user accepted the data policy.
func (u User) Consented() bool { return u.ConsentedAt != nil }

// Blocked reports whether the user is banned.
func (u User) Blocked() bool { return u.BlockedAt != nil }

// Location returns the user's timezone, or def if none is set or it is unknown.
func (u User) Location(def *time.Location) *time.Location {
	if u.Timezone == "" {
		return def
	}
	loc, err := time.LoadLocation(u.Timezone)
	if err != nil {
		return def
	}
	return loc
}

// Repository persists users and their family membership.
type Repository interface {
	// Admit returns the user with a refreshed name and last-seen time; names
	// change in Telegram. Someone new is created as the head of a new family.
	Admit(ctx context.Context, u User) (User, error)
	Get(ctx context.Context, telegramID int64) (User, error)
	// Family lists the members of a family, the head first.
	Family(ctx context.Context, familyID int64) ([]User, error)
	// All lists every user.
	All(ctx context.Context) ([]User, error)
	SetWeeklyReport(ctx context.Context, telegramID int64, on bool) (User, error)
	// SetTimezone stores an IANA timezone name; empty resets to the app default.
	SetTimezone(ctx context.Context, telegramID int64, tz string) (User, error)
	SetConsent(ctx context.Context, telegramID int64, at time.Time) (User, error)
	// Detach moves the user out of their family into a new family of their
	// own, where they are the head. Their expenses go with them.
	Detach(ctx context.Context, telegramID int64) (User, error)
	// Delete removes the user with all their data; a family left empty is
	// deleted too.
	Delete(ctx context.Context, telegramID int64) error
}
