// Package user describes family members: Telegram users who talk to the bot.
package user

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound means there is no user with the given Telegram ID.
var ErrNotFound = errors.New("user not found")

// User is a family member identified by their Telegram ID.
type User struct {
	TelegramID int64
	FirstName  string
	Username   string // may be empty: not every Telegram account has one
	IsHead     bool   // the family head cannot be removed from the panel
	RemovedAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Active reports whether the user is still a family member.
func (u User) Active() bool { return u.RemovedAt == nil }

// Repository persists users.
type Repository interface {
	// Upsert creates the user or refreshes their name, which may change in Telegram.
	// It does not change membership: a removed user stays removed.
	Upsert(ctx context.Context, u User) (User, error)
	// List returns all users, removed ones included: their expenses stay in history.
	List(ctx context.Context) ([]User, error)
	Get(ctx context.Context, telegramID int64) (User, error)
	// Add makes the user an active member, creating them if needed.
	Add(ctx context.Context, u User) (User, error)
	// Remove marks the user as removed; their expenses are kept.
	Remove(ctx context.Context, telegramID int64) error
	// CountActive returns the number of current family members.
	CountActive(ctx context.Context) (int, error)
}
