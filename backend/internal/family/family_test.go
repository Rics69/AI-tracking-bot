package family

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

type fakeUsers struct{ m map[int64]user.User }

func (f *fakeUsers) Upsert(_ context.Context, u user.User) (user.User, error) {
	old := f.m[u.TelegramID]
	old.TelegramID, old.FirstName, old.Username = u.TelegramID, u.FirstName, u.Username
	f.m[u.TelegramID] = old
	return old, nil
}

func (f *fakeUsers) List(context.Context) ([]user.User, error) { return nil, nil }

func (f *fakeUsers) Get(_ context.Context, id int64) (user.User, error) {
	u, ok := f.m[id]
	if !ok {
		return user.User{}, user.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) Add(_ context.Context, u user.User) (user.User, error) {
	u.IsHead = u.IsHead || f.m[u.TelegramID].IsHead
	u.RemovedAt = nil
	f.m[u.TelegramID] = u
	return u, nil
}

func (f *fakeUsers) Remove(_ context.Context, id int64) error {
	u, ok := f.m[id]
	if !ok {
		return user.ErrNotFound
	}
	now := time.Now()
	u.RemovedAt = &now
	f.m[id] = u
	return nil
}

func (f *fakeUsers) CountActive(context.Context) (int, error) {
	n := 0
	for _, u := range f.m {
		if u.Active() {
			n++
		}
	}
	return n, nil
}

type fakeInvites struct {
	users  *fakeUsers
	hashes [][]byte
	used   map[int]bool
}

func (f *fakeInvites) Create(_ context.Context, h []byte, exp time.Time) (Invite, error) {
	f.hashes = append(f.hashes, h)
	return Invite{ID: int64(len(f.hashes)), ExpiresAt: exp}, nil
}

func (f *fakeInvites) List(context.Context, time.Time) ([]Invite, error) { return nil, nil }
func (f *fakeInvites) Revoke(context.Context, int64) error               { return nil }

func (f *fakeInvites) Redeem(ctx context.Context, h []byte, u user.User) (user.User, error) {
	for i, known := range f.hashes {
		if bytes.Equal(known, h) && !f.used[i] {
			f.used[i] = true
			return f.users.Add(ctx, u)
		}
	}
	return user.User{}, ErrInviteInvalid
}

type fakeSettings map[string]string

func (f fakeSettings) Get(_ context.Context, k string) (string, bool, error) {
	v, ok := f[k]
	return v, ok, nil
}

func (f fakeSettings) Set(_ context.Context, k, v string) error { f[k] = v; return nil }

func newTestService(bootstrap ...int64) (*Service, *fakeUsers, fakeSettings) {
	users := &fakeUsers{m: map[int64]user.User{}}
	settings := fakeSettings{}
	return NewService(users, &fakeInvites{users: users, used: map[int]bool{}}, settings, bootstrap), users, settings
}

var anna = user.User{TelegramID: 1, FirstName: "Анна", Username: "anna"}

func TestClosedFamilyRefusesStrangers(t *testing.T) {
	s, _, _ := newTestService(99)
	if _, err := s.Admit(context.Background(), anna); !errors.Is(err, ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
}

func TestBootstrapMakesHeadsAndKeepsNames(t *testing.T) {
	s, users, _ := newTestService(1)
	ctx := context.Background()
	if err := s.Bootstrap(ctx, []int64{1}); err != nil {
		t.Fatal(err)
	}
	m, err := s.Admit(ctx, anna)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsHead || m.FirstName != "Анна" {
		t.Errorf("member = %+v, want the head with a refreshed name", m)
	}
	// A restart must not overwrite the real name with the placeholder.
	if err := s.Bootstrap(ctx, []int64{1}); err != nil {
		t.Fatal(err)
	}
	if users.m[1].FirstName != "Анна" {
		t.Errorf("name after re-bootstrap = %q", users.m[1].FirstName)
	}
}

func TestOpenFamilyFirstWriterIsHead(t *testing.T) {
	s, _, _ := newTestService()
	ctx := context.Background()
	first, err := s.Admit(ctx, anna)
	if err != nil || !first.IsHead {
		t.Fatalf("first = %+v, err = %v", first, err)
	}
	second, err := s.Admit(ctx, user.User{TelegramID: 2, FirstName: "Борис"})
	if err != nil || second.IsHead {
		t.Fatalf("second = %+v, err = %v", second, err)
	}
}

func TestRemovedMemberStaysOutEvenInOpenFamily(t *testing.T) {
	s, _, _ := newTestService()
	ctx := context.Background()
	_, _ = s.Admit(ctx, anna) // head
	boris := user.User{TelegramID: 2, FirstName: "Борис"}
	_, _ = s.Admit(ctx, boris)
	if err := s.Remove(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Admit(ctx, boris); !errors.Is(err, ErrNotMember) {
		t.Errorf("removed member admitted: err = %v", err)
	}
}

func TestHeadCannotBeRemoved(t *testing.T) {
	s, _, _ := newTestService(1)
	_ = s.Bootstrap(context.Background(), []int64{1})
	if err := s.Remove(context.Background(), 1); !errors.Is(err, ErrHeadRemoval) {
		t.Errorf("err = %v, want ErrHeadRemoval", err)
	}
	if err := s.Remove(context.Background(), 42); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("unknown user: err = %v", err)
	}
}

func TestInviteLifecycle(t *testing.T) {
	s, _, settings := newTestService(99)
	ctx := context.Background()

	if _, err := s.CreateInvite(ctx); !errors.Is(err, ErrBotUnknown) {
		t.Fatalf("without bot username: err = %v", err)
	}
	settings[SettingBotUsername] = "FamilyBot"
	inv, err := s.CreateInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, ok := strings.CutPrefix(inv.Link, "https://t.me/FamilyBot?start=")
	if !ok || len(code) < 20 || len(code) > 64 {
		t.Fatalf("link = %q", inv.Link)
	}
	if time.Until(inv.ExpiresAt) < InviteTTL-time.Minute {
		t.Errorf("expires at %s", inv.ExpiresAt)
	}

	m, joined, err := s.Join(ctx, code, anna)
	if err != nil || !joined || m.TelegramID != anna.TelegramID {
		t.Fatalf("join: %+v %v %v", m, joined, err)
	}
	if _, err := s.Admit(ctx, anna); err != nil {
		t.Errorf("new member not admitted: %v", err)
	}
	// Opening the link again as a member does nothing.
	if _, joined, err := s.Join(ctx, code, anna); err != nil || joined {
		t.Errorf("second join: joined=%v err=%v", joined, err)
	}
	// The code is single-use.
	if _, _, err := s.Join(ctx, code, user.User{TelegramID: 2}); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("reused code: err = %v", err)
	}
	if _, _, err := s.Join(ctx, "made-up", user.User{TelegramID: 3}); !errors.Is(err, ErrInviteInvalid) {
		t.Errorf("unknown code: err = %v", err)
	}
}

func TestInviteStatus(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	for want, inv := range map[InviteStatus]Invite{
		InviteActive:  {ExpiresAt: now.Add(time.Hour)},
		InviteExpired: {ExpiresAt: past},
		InviteUsed:    {ExpiresAt: now.Add(time.Hour), UsedAt: &past},
		InviteRevoked: {ExpiresAt: now.Add(time.Hour), RevokedAt: &past},
	} {
		if got := inv.Status(now); got != want {
			t.Errorf("status = %s, want %s", got, want)
		}
	}
}
