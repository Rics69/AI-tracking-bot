// Package family manages who sees whose expenses. Everyone starts in a family
// of their own; a family is joined by an invite link from its head, who then
// sees the expenses of every member. Expenses belong to the person: whoever
// leaves a family takes them along.
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
	ErrBlocked        = errors.New("the user is blocked")
	ErrNotMember      = errors.New("not a member of this family")
	ErrNotHead        = errors.New("only the family head can do this")
	ErrHeadRemoval    = errors.New("the family head cannot be removed")
	ErrHeadHasMembers = errors.New("the head of a family with members cannot leave it")
	ErrAlone          = errors.New("the user is the only member of their family")
	ErrAlreadyMember  = errors.New("already a member of this family")
	ErrInviteInvalid  = errors.New("invite is unknown, expired, revoked or already used")
	ErrInviteNotFound = errors.New("invite not found")
	ErrBotUnknown     = errors.New("the bot has not started yet, so its username for the link is unknown")
	ErrBadTimezone    = errors.New("unknown timezone")
)

// InviteStatus is the state of an invite at a moment in time.
type InviteStatus string

const (
	InviteActive  InviteStatus = "active"
	InviteUsed    InviteStatus = "used"
	InviteExpired InviteStatus = "expired"
	InviteRevoked InviteStatus = "revoked"
)

// Invite is a one-time link to join a family. The code itself is never stored.
type Invite struct {
	ID        int64
	FamilyID  int64
	CreatedBy *int64
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
	Create(ctx context.Context, familyID, createdBy int64, codeHash []byte, expiresAt time.Time) (Invite, error)
	// List returns the family's invites created after since, newest first.
	List(ctx context.Context, familyID int64, since time.Time) ([]Invite, error)
	// Revoke cancels an active invite of the family; ErrInviteNotFound if there is none.
	Revoke(ctx context.Context, familyID, id int64) error
	// Find returns the usable invite with this code hash; ErrInviteInvalid otherwise.
	Find(ctx context.Context, codeHash []byte) (Invite, error)
	// Redeem atomically uses a valid invite and moves the user into its family
	// as a member. It fails with ErrInviteInvalid, with ErrAlreadyMember (the
	// invite stays unused) or with ErrHeadHasMembers.
	Redeem(ctx context.Context, codeHash []byte, userID int64) (user.User, error)
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
	now      func() time.Time
}

func NewService(users user.Repository, invites Invites, settings Settings) *Service {
	return &Service{users: users, invites: invites, settings: settings, now: time.Now}
}

// Admit returns the person writing to the bot, creating them with a family of
// their own on the first message. A blocked user comes back with ErrBlocked.
func (s *Service) Admit(ctx context.Context, tg user.User) (user.User, error) {
	u, err := s.users.Admit(ctx, tg)
	if err != nil {
		return user.User{}, err
	}
	if u.Blocked() {
		return u, ErrBlocked
	}
	return u, nil
}

// Get returns a user by Telegram ID.
func (s *Service) Get(ctx context.Context, telegramID int64) (user.User, error) {
	return s.users.Get(ctx, telegramID)
}

// Consent records that the user accepted the data policy.
func (s *Service) Consent(ctx context.Context, telegramID int64) (user.User, error) {
	return s.users.SetConsent(ctx, telegramID, s.now())
}

// SetBlocked bans a user from the bot and the app, or lifts the ban. Their
// data stays: a ban is reversible, deleting is the user's own choice.
func (s *Service) SetBlocked(ctx context.Context, telegramID int64, on bool) (user.User, error) {
	var at *time.Time
	if on {
		now := s.now()
		at = &now
	}
	return s.users.SetBlocked(ctx, telegramID, at)
}

// Invitation checks an invite code and returns the head of the family it
// leads to, so the bot can ask the user before they join.
func (s *Service) Invitation(ctx context.Context, code string) (head user.User, err error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return user.User{}, ErrInviteInvalid
	}
	inv, err := s.invites.Find(ctx, hashCode(code))
	if err != nil {
		return user.User{}, err
	}
	members, err := s.users.Family(ctx, inv.FamilyID)
	if err != nil {
		return user.User{}, err
	}
	for _, m := range members {
		if m.IsHead {
			return m, nil
		}
	}
	return user.User{}, ErrInviteInvalid // a family without a head cannot be joined
}

// Join redeems an invite code: the user moves into the family as a member.
// A head with members must hand them off first (ErrHeadHasMembers).
func (s *Service) Join(ctx context.Context, code string, telegramID int64) (user.User, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return user.User{}, ErrInviteInvalid
	}
	return s.invites.Redeem(ctx, hashCode(code), telegramID)
}

// Leave takes a member out of their family into a family of their own.
func (s *Service) Leave(ctx context.Context, telegramID int64) (user.User, error) {
	u, err := s.users.Get(ctx, telegramID)
	if err != nil {
		return user.User{}, err
	}
	members, err := s.users.Family(ctx, u.FamilyID)
	if err != nil {
		return user.User{}, err
	}
	switch {
	case len(members) <= 1:
		return user.User{}, ErrAlone
	case u.IsHead:
		return user.User{}, ErrHeadHasMembers
	}
	return s.users.Detach(ctx, telegramID)
}

// Remove takes a member out of the head's family; they keep their expenses
// in a family of their own.
func (s *Service) Remove(ctx context.Context, head user.User, telegramID int64) error {
	if !head.IsHead {
		return ErrNotHead
	}
	m, err := s.Member(ctx, head.FamilyID, telegramID)
	if err != nil {
		return err
	}
	if m.IsHead {
		return ErrHeadRemoval
	}
	_, err = s.users.Detach(ctx, telegramID)
	return err
}

// TransferHead gives the head role to another member of the head's family.
func (s *Service) TransferHead(ctx context.Context, head user.User, telegramID int64) error {
	if !head.IsHead {
		return ErrNotHead
	}
	if telegramID == head.TelegramID {
		return nil
	}
	if _, err := s.Member(ctx, head.FamilyID, telegramID); err != nil {
		return err
	}
	if err := s.users.SetHead(ctx, head.FamilyID, telegramID); errors.Is(err, user.ErrNotFound) {
		return ErrNotMember // left the family meanwhile
	} else if err != nil {
		return err
	}
	return nil
}

// Member returns a member of the family; ErrNotMember if the user is in another one.
func (s *Service) Member(ctx context.Context, familyID, telegramID int64) (user.User, error) {
	u, err := s.users.Get(ctx, telegramID)
	if errors.Is(err, user.ErrNotFound) {
		return user.User{}, ErrNotMember
	}
	if err != nil {
		return user.User{}, err
	}
	if u.FamilyID != familyID {
		return user.User{}, ErrNotMember
	}
	return u, nil
}

// Members lists the family, the head first.
func (s *Service) Members(ctx context.Context, familyID int64) ([]user.User, error) {
	return s.users.Family(ctx, familyID)
}

// DeleteAccount removes the user with all their data. A head's family is
// dissolved first: every other member keeps their expenses in a family of
// their own.
func (s *Service) DeleteAccount(ctx context.Context, telegramID int64) error {
	u, err := s.users.Get(ctx, telegramID)
	if err != nil {
		return err
	}
	if u.IsHead {
		members, err := s.users.Family(ctx, u.FamilyID)
		if err != nil {
			return err
		}
		for _, m := range members {
			if m.TelegramID == u.TelegramID {
				continue
			}
			if _, err := s.users.Detach(ctx, m.TelegramID); err != nil {
				return fmt.Errorf("detach member %d: %w", m.TelegramID, err)
			}
		}
	}
	return s.users.Delete(ctx, telegramID)
}

// SetWeeklyReport turns the weekly summary on or off. by may change it for
// themselves or, as the head, for a member of their family.
func (s *Service) SetWeeklyReport(ctx context.Context, by user.User, telegramID int64, on bool) (user.User, error) {
	if telegramID != by.TelegramID {
		if !by.IsHead {
			return user.User{}, ErrNotHead
		}
		if _, err := s.Member(ctx, by.FamilyID, telegramID); err != nil {
			return user.User{}, err
		}
	}
	return s.users.SetWeeklyReport(ctx, telegramID, on)
}

// SetTimezone sets the user's timezone by IANA name ("Asia/Novosibirsk").
func (s *Service) SetTimezone(ctx context.Context, telegramID int64, tz string) (user.User, error) {
	if tz == "" || tz == "Local" {
		return user.User{}, ErrBadTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return user.User{}, ErrBadTimezone
	}
	return s.users.SetTimezone(ctx, telegramID, tz)
}

// CreateInvite issues a link to the head's family.
func (s *Service) CreateInvite(ctx context.Context, head user.User) (CreatedInvite, error) {
	if !head.IsHead {
		return CreatedInvite{}, ErrNotHead
	}
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
	inv, err := s.invites.Create(ctx, head.FamilyID, head.TelegramID, hashCode(code), s.now().Add(InviteTTL))
	if err != nil {
		return CreatedInvite{}, err
	}
	// Telegram passes ?start=<code> to the bot as "/start <code>".
	return CreatedInvite{Invite: inv, Link: "https://t.me/" + botName + "?start=" + code}, nil
}

// ListInvites returns the family's invites of the last month, newest first.
func (s *Service) ListInvites(ctx context.Context, familyID int64) ([]Invite, error) {
	return s.invites.List(ctx, familyID, s.now().Add(-30*24*time.Hour))
}

// RevokeInvite cancels an active invite of the family.
func (s *Service) RevokeInvite(ctx context.Context, familyID, id int64) error {
	return s.invites.Revoke(ctx, familyID, id)
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
