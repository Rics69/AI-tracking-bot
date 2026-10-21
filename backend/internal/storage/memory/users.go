package memory

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// Users keeps users, families and invites in memory with the semantics of the
// PostgreSQL repositories. It implements user.Repository and family.Invites,
// so tests can run the real family.Service.
type Users struct {
	mu         sync.Mutex
	users      map[int64]user.User
	invites    []invite
	nextFamily int64
	// Expenses, if set, learns every family change, so InFamily scopes follow
	// membership like the SQL subquery does.
	Expenses *ExpenseRepository
	now      func() time.Time
}

type invite struct {
	family.Invite
	hash []byte
}

var (
	_ user.Repository = (*Users)(nil)
	_ family.Invites  = (*Users)(nil)
	_ family.Settings = (*Settings)(nil)
)

func NewUsers() *Users {
	return &Users{users: map[int64]user.User{}, now: time.Now}
}

func (r *Users) Admit(_ context.Context, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	saved, ok := r.users[u.TelegramID]
	if !ok {
		saved = user.User{
			TelegramID: u.TelegramID, FamilyID: r.newFamily(), IsHead: true,
			WeeklyReport: true, CreatedAt: now,
		}
	}
	if saved.FirstName != u.FirstName || saved.Username != u.Username {
		saved.FirstName, saved.Username, saved.UpdatedAt = u.FirstName, u.Username, now
	}
	saved.LastSeenAt = &now
	r.store(saved)
	return saved, nil
}

func (r *Users) Get(_ context.Context, id int64) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	return u, nil
}

func (r *Users) Family(_ context.Context, familyID int64) ([]user.User, error) {
	return r.list(func(u user.User) bool { return u.FamilyID == familyID }), nil
}

func (r *Users) All(context.Context) ([]user.User, error) {
	return r.list(func(user.User) bool { return true }), nil
}

func (r *Users) list(keep func(user.User) bool) []user.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []user.User
	for _, u := range r.users {
		if keep(u) {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b user.User) int {
		if c := cmp.Compare(a.FamilyID, b.FamilyID); c != 0 {
			return c
		}
		if a.IsHead != b.IsHead {
			if a.IsHead {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.FirstName, b.FirstName); c != 0 {
			return c
		}
		return cmp.Compare(a.TelegramID, b.TelegramID)
	})
	return out
}

func (r *Users) SetWeeklyReport(_ context.Context, id int64, on bool) (user.User, error) {
	return r.update(id, func(u *user.User) { u.WeeklyReport = on })
}

func (r *Users) SetTimezone(_ context.Context, id int64, tz string) (user.User, error) {
	return r.update(id, func(u *user.User) { u.Timezone = tz })
}

func (r *Users) SetConsent(_ context.Context, id int64, at time.Time) (user.User, error) {
	return r.update(id, func(u *user.User) { u.ConsentedAt = &at })
}

// Block bans a user; there is no command for it yet, as in the database.
func (r *Users) Block(id int64) {
	_, _ = r.update(id, func(u *user.User) { now := r.now(); u.BlockedAt = &now })
}

func (r *Users) update(id int64, fn func(*user.User)) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	fn(&u)
	u.UpdatedAt = r.now()
	r.store(u)
	return u, nil
}

func (r *Users) Detach(_ context.Context, id int64) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	u.FamilyID, u.IsHead = r.newFamily(), true
	r.store(u)
	return u, nil
}

func (r *Users) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[id]; !ok {
		return user.ErrNotFound
	}
	delete(r.users, id)
	if r.Expenses != nil {
		r.Expenses.deleteOwner(id)
	}
	return nil
}

// store saves u and tells the expense repository about its family; r.mu is held.
func (r *Users) store(u user.User) {
	r.users[u.TelegramID] = u
	if r.Expenses != nil {
		r.Expenses.SetFamily(u.TelegramID, u.FamilyID)
	}
}

func (r *Users) newFamily() int64 {
	r.nextFamily++
	return r.nextFamily
}

// Invites.

func (r *Users) Create(_ context.Context, familyID, createdBy int64, hash []byte, expiresAt time.Time) (family.Invite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv := family.Invite{
		ID: int64(len(r.invites) + 1), FamilyID: familyID, CreatedBy: &createdBy,
		CreatedAt: r.now(), ExpiresAt: expiresAt,
	}
	r.invites = append(r.invites, invite{Invite: inv, hash: hash})
	return inv, nil
}

func (r *Users) List(_ context.Context, familyID int64, since time.Time) ([]family.Invite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []family.Invite
	for i := len(r.invites) - 1; i >= 0; i-- {
		if inv := r.invites[i]; inv.FamilyID == familyID && !inv.CreatedAt.Before(since) {
			out = append(out, inv.Invite)
		}
	}
	return out, nil
}

func (r *Users) Revoke(_ context.Context, familyID, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.invites {
		inv := &r.invites[i]
		if inv.ID == id && inv.FamilyID == familyID && inv.Status(r.now()) == family.InviteActive {
			now := r.now()
			inv.RevokedAt = &now
			return nil
		}
	}
	return family.ErrInviteNotFound
}

func (r *Users) Find(_ context.Context, hash []byte) (family.Invite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inv := r.usable(hash); inv != nil {
		return inv.Invite, nil
	}
	return family.Invite{}, family.ErrInviteInvalid
}

func (r *Users) Redeem(_ context.Context, hash []byte, userID int64) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inv := r.usable(hash)
	if inv == nil {
		return user.User{}, family.ErrInviteInvalid
	}
	u, ok := r.users[userID]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	if u.FamilyID == inv.FamilyID {
		return user.User{}, family.ErrAlreadyMember
	}
	if u.IsHead {
		for _, m := range r.users {
			if m.FamilyID == u.FamilyID && m.TelegramID != u.TelegramID {
				return user.User{}, family.ErrHeadHasMembers
			}
		}
	}
	u.FamilyID, u.IsHead = inv.FamilyID, false
	r.store(u)
	now := r.now()
	inv.UsedAt, inv.UsedBy = &now, &userID
	return u, nil
}

func (r *Users) usable(hash []byte) *invite {
	for i := range r.invites {
		if inv := &r.invites[i]; bytes.Equal(inv.hash, hash) && inv.Status(r.now()) == family.InviteActive {
			return inv
		}
	}
	return nil
}

// Settings is an in-memory family.Settings.
type Settings struct {
	mu sync.Mutex
	m  map[string]string
}

func NewSettings() *Settings { return &Settings{m: map[string]string{}} }

func (s *Settings) Get(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *Settings) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}
