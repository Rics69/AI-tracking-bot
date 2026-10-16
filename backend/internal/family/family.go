// Package family manages who belongs to the family: invite links, joining
// through the bot, removing members, and deciding whom the bot talks to.
package family

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// InviteTTL is how long an invite link stays valid.
const InviteTTL = 7 * 24 * time.Hour

// SettingBotUsername is the settings key under which the bot stores its @username.
const SettingBotUsername = "telegram_bot_username"

var (
	ErrNotMember      = errors.New("not a family member")
	ErrInviteInvalid  = errors.New("invite is unknown, expired, revoked or already used")
	ErrInviteNotFound = errors.New("invite not found")
	ErrHeadRemoval    = errors.New("the family head cannot be removed")
	ErrBotUnknown     = errors.New("the bot has not started yet, so its username for the link is unknown")
)

// InviteStatus is the state of an invite at a moment in time.
type InviteStatus string

const (
	InviteActive  InviteStatus = "active"
	InviteUsed    InviteStatus = "used"
	InviteExpired InviteStatus = "expired"
	InviteRevoked InviteStatus = "revoked"
)

// Invite is a one-time link to join the family. The code itself is never stored.
type Invite struct {
	ID        int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	UsedBy    *int64
	RevokedAt *time.Time
}

// Status reports the state of the invite at now.
func (i Invite) Status(now time.Time) InviteStatus {
	switch {
	case i.RevokedAt != nil:
		return InviteRevoked
	case i.UsedAt != nil:
		return InviteUsed
	case !now.Before(i.ExpiresAt):
		return InviteExpired
	default:
		return InviteActive
	}
}

// CreatedInvite is a new invite with its link: the link is shown only once.
type CreatedInvite struct {
	Invite
	Link string
}

// Invites persists invites.
type Invites interface {
	Create(ctx context.Context, codeHash []byte, expiresAt time.Time) (Invite, error)
	// List returns invites created after since, newest first.
	List(ctx context.Context, since time.Time) ([]Invite, error)
	// Revoke cancels an active invite; ErrInviteNotFound if there is none.
	Revoke(ctx context.Context, id int64) error
	// Redeem atomically marks a valid invite as used and adds u to the family;
	// ErrInviteInvalid if the invite cannot be used.
	Redeem(ctx context.Context, codeHash []byte, u user.User) (user.User, error)
}

// Settings is a small key-value store.
type Settings interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// Service implements family membership.
type Service struct {
	users    user.Repository
	invites  Invites
	settings Settings
	// open: no bootstrap members configured, so anyone who writes to the bot
	// joins (handy for a demo; set ALLOWED_TELEGRAM_IDS in production).
	open bool
	now  func() time.Time
}

// NewService creates the service. With no bootstrap IDs the family is open.
func NewService(users user.Repository, invites Invites, settings Settings, bootstrapIDs []int64) *Service {
	return &Service{users: users, invites: invites, settings: settings, open: len(bootstrapIDs) == 0, now: time.Now}
}

// Bootstrap makes the configured IDs family heads, so a fresh install has
// someone who can invite the rest.
func (s *Service) Bootstrap(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		u := user.User{TelegramID: id, FirstName: "Глава семьи", IsHead: true}
		if existing, err := s.users.Get(ctx, id); err == nil {
			u.FirstName, u.Username = existing.FirstName, existing.Username
		} else if !errors.Is(err, user.ErrNotFound) {
			return err
		}
		if _, err := s.users.Add(ctx, u); err != nil {
			return fmt.Errorf("bootstrap member %d: %w", id, err)
		}
	}
	return nil
}

// Admit decides whether the bot talks to a Telegram user and returns them as
// a member, refreshing their name. ErrNotMember means the bot must refuse.
func (s *Service) Admit(ctx context.Context, tg user.User) (user.User, error) {
	m, err := s.users.Get(ctx, tg.TelegramID)
	switch {
	case err == nil && m.Active():
		if m.FirstName == tg.FirstName && m.Username == tg.Username {
			return m, nil
		}
		return s.users.Upsert(ctx, tg) // names change in Telegram; the panel shows them
	case err == nil:
		return user.User{}, ErrNotMember // removed: stays out even in open mode
	case !errors.Is(err, user.ErrNotFound):
		return user.User{}, err
	case !s.open:
		return user.User{}, ErrNotMember
	}
	// Open family: the first person to write becomes the head.
	n, err := s.users.CountActive(ctx)
	if err != nil {
		return user.User{}, err
	}
	tg.IsHead = n == 0
	return s.users.Add(ctx, tg)
}

// Join redeems an invite code. joined is false when the user already was a
// member: the invite then stays unused for whoever it was meant for.
func (s *Service) Join(ctx context.Context, code string, tg user.User) (member user.User, joined bool, err error) {
	if m, err := s.users.Get(ctx, tg.TelegramID); err == nil && m.Active() {
		return m, false, nil
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return user.User{}, false, ErrInviteInvalid
	}
	m, err := s.invites.Redeem(ctx, hashCode(code), user.User{
		TelegramID: tg.TelegramID, FirstName: tg.FirstName, Username: tg.Username,
	})
	if err != nil {
		return user.User{}, false, err
	}
	return m, true, nil
}

// CreateInvite issues a new invite link to the bot.
func (s *Service) CreateInvite(ctx context.Context) (CreatedInvite, error) {
	botName, ok, err := s.settings.Get(ctx, SettingBotUsername)
	if err != nil {
		return CreatedInvite{}, err
	}
	if !ok || botName == "" {
		return CreatedInvite{}, ErrBotUnknown
	}
	code, err := newCode()
	if err != nil {
		return CreatedInvite{}, err
	}
	inv, err := s.invites.Create(ctx, hashCode(code), s.now().Add(InviteTTL))
	if err != nil {
		return CreatedInvite{}, err
	}
	// Telegram passes ?start=<code> to the bot as "/start <code>".
	return CreatedInvite{Invite: inv, Link: "https://t.me/" + botName + "?start=" + code}, nil
}

// ListInvites returns the invites of the last month, newest first.
func (s *Service) ListInvites(ctx context.Context) ([]Invite, error) {
	return s.invites.List(ctx, s.now().Add(-30*24*time.Hour))
}

// RevokeInvite cancels an active invite.
func (s *Service) RevokeInvite(ctx context.Context, id int64) error {
	return s.invites.Revoke(ctx, id)
}

// Remove takes a member out of the family; their expenses stay in history.
func (s *Service) Remove(ctx context.Context, telegramID int64) error {
	m, err := s.users.Get(ctx, telegramID)
	if err != nil {
		return err
	}
	if m.IsHead {
		return ErrHeadRemoval
	}
	return s.users.Remove(ctx, telegramID)
}

// SetWeeklyReport turns the weekly summary in the bot on or off for a member.
func (s *Service) SetWeeklyReport(ctx context.Context, telegramID int64, on bool) (user.User, error) {
	return s.users.SetWeeklyReport(ctx, telegramID, on)
}

// Members lists everyone who has been in the family, removed members included.
func (s *Service) Members(ctx context.Context) ([]user.User, error) {
	return s.users.List(ctx)
}

// SetBotUsername remembers the bot's @username for invite links.
func (s *Service) SetBotUsername(ctx context.Context, username string) error {
	return s.settings.Set(ctx, SettingBotUsername, username)
}

// Now is the service clock, exposed so callers compute invite status consistently.
func (s *Service) Now() time.Time { return s.now() }

// newCode returns 128 random bits in a form Telegram accepts as a start
// parameter ([A-Za-z0-9_-], up to 64 characters).
func newCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashCode(code string) []byte {
	h := sha256.Sum256([]byte(code))
	return h[:]
}
