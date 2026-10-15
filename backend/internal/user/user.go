// Package user describes family members: Telegram users who talk to the bot.
package user

import (
	"context"
	"time"
)

// User is a family member identified by their Telegram ID.
type User struct {
	TelegramID int64
	FirstName  string
	Username   string // may be empty: not every Telegram account has one
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Repository persists users.
type Repository interface {
	// Upsert creates the user or refreshes their name, which may change in Telegram.
	Upsert(ctx context.Context, u User) (User, error)
	List(ctx context.Context) ([]User, error)
}
