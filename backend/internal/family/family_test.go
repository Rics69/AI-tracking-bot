package family_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/memory"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

var (
	anna  = user.User{TelegramID: 1, FirstName: "Анна", Username: "anna"}
	boris = user.User{TelegramID: 2, FirstName: "Борис"}
	vera  = user.User{TelegramID: 3, FirstName: "Вера"}
)

func newTestService(t *testing.T) (*family.Service, *memory.Users) {
	t.Helper()
	users := memory.NewUsers()
	settings := memory.NewSettings()
	if err := settings.Set(context.Background(), family.SettingBotUsername, "SavepointBot"); err != nil {
		t.Fatal(err)
	}
	return family.NewService(users, users, settings), users
}

func admit(t *testing.T, s *family.Service, u user.User) user.User {
	t.Helper()
	m, err := s.Admit(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// invite returns the code of a new invite to head's family.
func invite(t *testing.T, s *family.Service, head user.User) string {
	t.Helper()
	inv, err := s.CreateInvite(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	code, ok := strings.CutPrefix(inv.Link, "https://t.me/SavepointBot?start=")
	if !ok || len(code) < 20 || len(code) > 64 {
		t.Fatalf("link = %q", inv.Link)
	}
	return code
}

func TestEveryoneStartsInAFamilyOfTheirOwn(t *testing.T) {
	s, _ := newTestService(t)
	a, b := admit(t, s, anna), admit(t, s, boris)
	if !a.IsHead || !b.IsHead || a.FamilyID == b.FamilyID {
		t.Errorf("anna = %+v, boris = %+v: want two separate one-person families", a, b)
	}
	if a.Consented() {
		t.Error("a new user must accept the data policy first")
	}
	again := admit(t, s, user.User{TelegramID: 1, FirstName: "Аня"})
	if again.FamilyID != a.FamilyID || again.FirstName != "Аня" {
		t.Errorf("second message: %+v, want the same family and a refreshed name", again)
	}
}

func TestJoinByInvite(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	head := admit(t, s, anna)
	admit(t, s, boris)
	code := invite(t, s, head)

	inviter, err := s.Invitation(ctx, code)
	if err != nil || inviter.TelegramID != anna.TelegramID {
		t.Fatalf("invitation: %+v %v", inviter, err)
	}
	m, err := s.Join(ctx, code, boris.TelegramID)
	if err != nil || m.FamilyID != head.FamilyID || m.IsHead {
		t.Fatalf("join: %+v %v", m, err)
	}
	if members, _ := s.Members(ctx, head.FamilyID); len(members) != 2 || members[0].TelegramID != anna.TelegramID {
		t.Errorf("members = %+v, want Anna (head) first, then Boris", members)
	}
	// The code is single-use.
	admit(t, s, vera)
	if _, err := s.Join(ctx, code, vera.TelegramID); !errors.Is(err, family.ErrInviteInvalid) {
		t.Errorf("reused code: err = %v", err)
	}
	if _, err := s.Invitation(ctx, "made-up"); !errors.Is(err, family.ErrInviteInvalid) {
		t.Errorf("unknown code: err = %v", err)
	}
	// A member opening a new link to their own family stays put; the link stays unused.
	again := invite(t, s, head)
	if _, err := s.Join(ctx, again, boris.TelegramID); !errors.Is(err, family.ErrAlreadyMember) {
		t.Errorf("member joins own family: err = %v", err)
	}
	if _, err := s.Join(ctx, again, vera.TelegramID); err != nil {
		t.Errorf("the unused link must still work: %v", err)
	}
}

func TestHeadWithMembersCannotJoinAnotherFamily(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	a, v := admit(t, s, anna), admit(t, s, vera)
	admit(t, s, boris)
	if _, err := s.Join(ctx, invite(t, s, a), boris.TelegramID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(ctx, invite(t, s, v), anna.TelegramID); !errors.Is(err, family.ErrHeadHasMembers) {
		t.Errorf("err = %v, want family.ErrHeadHasMembers", err)
	}
	// A member may switch families.
	if m, err := s.Join(ctx, invite(t, s, v), boris.TelegramID); err != nil || m.FamilyID != v.FamilyID {
		t.Errorf("member switches: %+v %v", m, err)
	}
}

func TestLeaveAndRemove(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	head := admit(t, s, anna)
	admit(t, s, boris)
	admit(t, s, vera)
	if _, err := s.Leave(ctx, anna.TelegramID); !errors.Is(err, family.ErrAlone) {
		t.Errorf("alone: err = %v", err)
	}
	for _, u := range []user.User{boris, vera} {
		if _, err := s.Join(ctx, invite(t, s, head), u.TelegramID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Leave(ctx, anna.TelegramID); !errors.Is(err, family.ErrHeadHasMembers) {
		t.Errorf("head leaves: err = %v", err)
	}

	b, err := s.Leave(ctx, boris.TelegramID)
	if err != nil || !b.IsHead || b.FamilyID == head.FamilyID {
		t.Errorf("Boris leaves: %+v %v, want a family of his own", b, err)
	}
	member, _ := s.Get(ctx, vera.TelegramID)
	if err := s.Remove(ctx, member, anna.TelegramID); !errors.Is(err, family.ErrNotHead) {
		t.Errorf("member removes the head: err = %v", err)
	}
	if err := s.Remove(ctx, head, anna.TelegramID); !errors.Is(err, family.ErrHeadRemoval) {
		t.Errorf("head removes self: err = %v", err)
	}
	if err := s.Remove(ctx, head, boris.TelegramID); !errors.Is(err, family.ErrNotMember) {
		t.Errorf("head removes a stranger: err = %v", err)
	}
	if err := s.Remove(ctx, head, vera.TelegramID); err != nil {
		t.Fatal(err)
	}
	if members, _ := s.Members(ctx, head.FamilyID); len(members) != 1 {
		t.Errorf("members after removal = %+v", members)
	}
}

func TestDeleteAccountDissolvesTheFamily(t *testing.T) {
	s, users := newTestService(t)
	ctx := context.Background()
	head := admit(t, s, anna)
	admit(t, s, boris)
	if _, err := s.Join(ctx, invite(t, s, head), boris.TelegramID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, anna.TelegramID); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Get(ctx, anna.TelegramID); !errors.Is(err, user.ErrNotFound) {
		t.Errorf("deleted user still there: %v", err)
	}
	b, _ := users.Get(ctx, boris.TelegramID)
	if !b.IsHead || b.FamilyID == head.FamilyID {
		t.Errorf("Boris after the head left: %+v, want a family of his own", b)
	}
}

func TestOnlyTheHeadManagesTheFamily(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	head := admit(t, s, anna)
	admit(t, s, boris)
	stranger := admit(t, s, vera)
	if _, err := s.Join(ctx, invite(t, s, head), boris.TelegramID); err != nil {
		t.Fatal(err)
	}
	member, _ := s.Get(ctx, boris.TelegramID)
	if _, err := s.CreateInvite(ctx, member); !errors.Is(err, family.ErrNotHead) {
		t.Errorf("member invites: err = %v", err)
	}
	if _, err := s.SetWeeklyReport(ctx, member, anna.TelegramID, false); !errors.Is(err, family.ErrNotHead) {
		t.Errorf("member changes the head's settings: err = %v", err)
	}
	if u, err := s.SetWeeklyReport(ctx, member, boris.TelegramID, false); err != nil || u.WeeklyReport {
		t.Errorf("own setting: %+v %v", u, err)
	}
	if _, err := s.SetWeeklyReport(ctx, head, boris.TelegramID, true); err != nil {
		t.Errorf("head changes a member's setting: %v", err)
	}
	if _, err := s.SetWeeklyReport(ctx, head, stranger.TelegramID, false); !errors.Is(err, family.ErrNotMember) {
		t.Errorf("head changes a stranger's setting: err = %v", err)
	}
	invites, _ := s.ListInvites(ctx, head.FamilyID)
	if err := s.RevokeInvite(ctx, stranger.FamilyID, invites[0].ID); !errors.Is(err, family.ErrInviteNotFound) {
		t.Errorf("revoke another family's invite: err = %v", err)
	}
}

func TestInviteNeedsBotUsername(t *testing.T) {
	users := memory.NewUsers()
	s := family.NewService(users, users, memory.NewSettings())
	head := admit(t, s, anna)
	if _, err := s.CreateInvite(context.Background(), head); !errors.Is(err, family.ErrBotUnknown) {
		t.Errorf("err = %v, want family.ErrBotUnknown", err)
	}
}

func TestBlockedUserIsRefused(t *testing.T) {
	s, users := newTestService(t)
	admit(t, s, anna)
	users.Block(anna.TelegramID)
	if _, err := s.Admit(context.Background(), anna); !errors.Is(err, family.ErrBlocked) {
		t.Errorf("err = %v, want family.ErrBlocked", err)
	}
}

func TestTimezone(t *testing.T) {
	s, _ := newTestService(t)
	admit(t, s, anna)
	ctx := context.Background()
	u, err := s.SetTimezone(ctx, anna.TelegramID, "Asia/Novosibirsk")
	if err != nil || u.Location(time.UTC).String() != "Asia/Novosibirsk" {
		t.Errorf("set: %+v %v", u, err)
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus"} {
		if _, err := s.SetTimezone(ctx, anna.TelegramID, bad); !errors.Is(err, family.ErrBadTimezone) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestInviteStatus(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	for want, inv := range map[family.InviteStatus]family.Invite{
		family.InviteActive:  {ExpiresAt: now.Add(time.Hour)},
		family.InviteExpired: {ExpiresAt: past},
		family.InviteUsed:    {ExpiresAt: now.Add(time.Hour), UsedAt: &past},
		family.InviteRevoked: {ExpiresAt: now.Add(time.Hour), RevokedAt: &past},
	} {
		if got := inv.Status(now); got != want {
			t.Errorf("status = %s, want %s", got, want)
		}
	}
}

func TestTransferHead(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	head := admit(t, s, anna)
	admit(t, s, boris)
	stranger := admit(t, s, vera)
	if _, err := s.Join(ctx, invite(t, s, head), boris.TelegramID); err != nil {
		t.Fatal(err)
	}
	member, _ := s.Get(ctx, boris.TelegramID)
	if err := s.TransferHead(ctx, member, anna.TelegramID); !errors.Is(err, family.ErrNotHead) {
		t.Errorf("member transfers: err = %v", err)
	}
	if err := s.TransferHead(ctx, head, stranger.TelegramID); !errors.Is(err, family.ErrNotMember) {
		t.Errorf("transfer to a stranger: err = %v", err)
	}
	if err := s.TransferHead(ctx, head, boris.TelegramID); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get(ctx, anna.TelegramID)
	b, _ := s.Get(ctx, boris.TelegramID)
	if a.IsHead || !b.IsHead || a.FamilyID != b.FamilyID {
		t.Errorf("after transfer: anna = %+v, boris = %+v", a, b)
	}
	// Now Anna is a member and may leave.
	if _, err := s.Leave(ctx, anna.TelegramID); err != nil {
		t.Errorf("former head leaves: %v", err)
	}
}
