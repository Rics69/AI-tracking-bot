package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

type fakeSender struct {
	mu       sync.Mutex
	sent     []string
	failures int // the first N sends fail with a network error
	attempts int
}

func (f *fakeSender) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failures {
		return nil, errors.New("unexpected EOF")
	}
	f.sent = append(f.sent, p.Text)
	return &models.Message{}, nil
}

func (f *fakeSender) SendChatAction(context.Context, *bot.SendChatActionParams) (bool, error) {
	return true, nil
}

type fakeAgent struct {
	reply   string
	err     error
	callers []agent.Caller
	resets  []int64
}

func (f *fakeAgent) Reply(_ context.Context, c agent.Caller, _ string) (string, error) {
	f.callers = append(f.callers, c)
	return f.reply, f.err
}

func (f *fakeAgent) Reset(_ context.Context, id int64) { f.resets = append(f.resets, id) }

// fakeFamily admits members, records who was checked and accepts one invite code.
type fakeFamily struct {
	members map[int64]bool
	admits  []user.User
	code    string
	err     error
}

func (f *fakeFamily) Admit(_ context.Context, tg user.User) (user.User, error) {
	f.admits = append(f.admits, tg)
	if f.err != nil {
		return user.User{}, f.err
	}
	if !f.members[tg.TelegramID] {
		return user.User{}, family.ErrNotMember
	}
	return tg, nil
}

func (f *fakeFamily) Join(_ context.Context, code string, tg user.User) (user.User, bool, error) {
	if f.members[tg.TelegramID] {
		return tg, false, nil
	}
	if code != f.code {
		return user.User{}, false, family.ErrInviteInvalid
	}
	f.members[tg.TelegramID] = true
	return tg, true, nil
}

func privateMessage(fromID int64, text string) *models.Update {
	return &models.Update{Message: &models.Message{
		From: &models.User{ID: fromID, FirstName: "Анна", Username: "anna"},
		Chat: models.Chat{ID: fromID, Type: models.ChatTypePrivate},
		Text: text,
	}}
}

// setup returns a handler where user 42 is a family member.
func setup() (*Handler, *fakeAgent, *fakeFamily, *fakeSender) {
	a := &fakeAgent{reply: "Записал"}
	f := &fakeFamily{members: map[int64]bool{42: true}, code: "secret"}
	h := NewHandler(a, f, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.retryDelay = time.Millisecond
	return h, a, f, &fakeSender{}
}

func TestMessageGoesToAgentAsSender(t *testing.T) {
	h, a, f, s := setup()
	h.handle(context.Background(), s, privateMessage(42, "кофе 350"))

	if len(a.callers) != 1 || a.callers[0].UserID != 42 || a.callers[0].Name != "Анна" {
		t.Errorf("agent callers = %+v", a.callers)
	}
	if len(f.admits) != 1 || f.admits[0].TelegramID != 42 || f.admits[0].Username != "anna" {
		t.Errorf("membership checks = %+v", f.admits)
	}
	if len(s.sent) != 1 || s.sent[0] != "Записал" {
		t.Errorf("sent = %q", s.sent)
	}
}

func TestStrangerIsRefused(t *testing.T) {
	h, a, _, s := setup()
	h.handle(context.Background(), s, privateMessage(7, "кофе 350"))
	if len(a.callers) != 0 {
		t.Error("stranger reached the agent")
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0], "ссылку-приглашение") || !strings.Contains(s.sent[0], "7") {
		t.Errorf("sent = %q, want a refusal with the user's ID", s.sent)
	}
}

func TestJoinByInvite(t *testing.T) {
	h, a, f, s := setup()
	ctx := context.Background()

	h.handle(ctx, s, privateMessage(7, "/start wrong"))
	h.handle(ctx, s, privateMessage(7, "/start secret"))
	h.handle(ctx, s, privateMessage(7, "кофе 350"))
	h.handle(ctx, s, privateMessage(42, "/start secret")) // already a member

	if len(s.sent) != 4 {
		t.Fatalf("sent = %q", s.sent)
	}
	if s.sent[0] != replyInviteInvalid || !strings.HasPrefix(s.sent[1], "Анна, добро пожаловать") ||
		s.sent[2] != "Записал" || !strings.HasPrefix(s.sent[3], "Вы уже в семье") {
		t.Errorf("sent = %q", s.sent)
	}
	if !f.members[7] || len(a.callers) != 1 {
		t.Errorf("members = %v, agent calls = %d", f.members, len(a.callers))
	}
}

func TestMembershipCheckFailure(t *testing.T) {
	h, a, f, s := setup()
	f.err = errors.New("db down")
	h.handle(context.Background(), s, privateMessage(42, "кофе 350"))
	if len(a.callers) != 0 || len(s.sent) != 1 || s.sent[0] != replyFailure {
		t.Errorf("agent calls = %d, sent = %q", len(a.callers), s.sent)
	}
}

func TestStartResetsConversation(t *testing.T) {
	h, a, _, s := setup()
	h.handle(context.Background(), s, privateMessage(42, "/start"))
	if len(a.callers) != 0 || len(a.resets) != 1 || s.sent[0] != replyHelp {
		t.Errorf("callers = %d resets = %v sent = %q", len(a.callers), a.resets, s.sent)
	}
}

func TestIgnoredAndRejectedMessages(t *testing.T) {
	group := privateMessage(42, "кофе 350")
	group.Message.Chat.Type = models.ChatTypeGroup

	tests := []struct {
		name   string
		update *models.Update
		want   string // "" means no reply
	}{
		{"edited message", &models.Update{EditedMessage: &models.Message{}}, ""},
		{"group chat", group, replyPrivate},
		{"sticker", privateMessage(42, ""), replyNotText},
		{"too long", privateMessage(42, strings.Repeat("а", maxInputRunes+1)), replyTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, a, _, s := setup()
			h.handle(context.Background(), s, tt.update)
			if len(a.callers) != 0 {
				t.Error("message reached the agent")
			}
			if tt.want == "" && len(s.sent) != 0 || tt.want != "" && (len(s.sent) != 1 || s.sent[0] != tt.want) {
				t.Errorf("sent = %q, want %q", s.sent, tt.want)
			}
		})
	}
}

func TestAgentFailure(t *testing.T) {
	h, a, _, s := setup()
	a.err = errors.New("gigachat: HTTP 500")
	h.handle(context.Background(), s, privateMessage(42, "кофе 350"))
	if len(s.sent) != 1 || s.sent[0] != replyFailure {
		t.Errorf("sent = %q", s.sent)
	}
}

func TestIsCommand(t *testing.T) {
	for text, want := range map[string]bool{
		"/start": true, "/start@FamilyBot": true, "/START": true, "/start hello": true,
		"/stats": false, "start": false,
	} {
		if got := isCommand(text, "/start"); got != want {
			t.Errorf("isCommand(%q) = %v", text, got)
		}
	}
}

func TestReplyIsResentAfterNetworkFailure(t *testing.T) {
	h, _, _, s := setup()
	s.failures = 2
	h.handle(context.Background(), s, privateMessage(42, "кофе 350"))
	if s.attempts != 3 || len(s.sent) != 1 || s.sent[0] != "Записал" {
		t.Errorf("attempts = %d sent = %q", s.attempts, s.sent)
	}
}

func TestReplyIsSentAfterRequestContextExpired(t *testing.T) {
	h, _, _, s := setup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // e.g. the LLM call ran out of time
	h.send(ctx, s, 42, replyFailure)
	if len(s.sent) != 1 {
		t.Errorf("sent = %q, want the failure message delivered", s.sent)
	}
}

func TestPermanentErrorsAreNotRetried(t *testing.T) {
	h, _, _, _ := setup()
	s := &blockedSender{}
	err := h.Send(context.Background(), s, 42, "x")
	if !errors.Is(err, ErrUndeliverable) || s.attempts != 1 {
		t.Errorf("err = %v after %d attempts, want ErrUndeliverable after 1", err, s.attempts)
	}
}

// blockedSender behaves like Telegram for a user who blocked the bot.
type blockedSender struct{ attempts int }

func (b *blockedSender) SendMessage(context.Context, *bot.SendMessageParams) (*models.Message, error) {
	b.attempts++
	return nil, fmt.Errorf("%w, Forbidden: bot was blocked by the user", bot.ErrorForbidden)
}

func (b *blockedSender) SendChatAction(context.Context, *bot.SendChatActionParams) (bool, error) {
	return true, nil
}

func TestSendGivesUp(t *testing.T) {
	h, _, _, s := setup()
	s.failures = 100
	h.send(context.Background(), s, 42, "x")
	if s.attempts != sendAttempts || len(s.sent) != 0 {
		t.Errorf("attempts = %d", s.attempts)
	}
}
