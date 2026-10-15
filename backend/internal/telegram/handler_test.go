package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeSender) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeAgent) Reset(id int64) { f.resets = append(f.resets, id) }

type fakeUsers struct{ saved []user.User }

func (f *fakeUsers) Upsert(_ context.Context, u user.User) (user.User, error) {
	f.saved = append(f.saved, u)
	return u, nil
}

func privateMessage(fromID int64, text string) *models.Update {
	return &models.Update{Message: &models.Message{
		From: &models.User{ID: fromID, FirstName: "Анна", Username: "anna"},
		Chat: models.Chat{ID: fromID, Type: models.ChatTypePrivate},
		Text: text,
	}}
}

func setup(allowed ...int64) (*Handler, *fakeAgent, *fakeUsers, *fakeSender) {
	a := &fakeAgent{reply: "Записал"}
	u := &fakeUsers{}
	h := NewHandler(a, u, allowed, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h, a, u, &fakeSender{}
}

func TestMessageGoesToAgentAsSender(t *testing.T) {
	h, a, users, s := setup()
	h.handle(context.Background(), s, privateMessage(42, "кофе 350"))

	if len(a.callers) != 1 || a.callers[0].UserID != 42 || a.callers[0].Name != "Анна" {
		t.Errorf("agent callers = %+v", a.callers)
	}
	if len(users.saved) != 1 || users.saved[0].TelegramID != 42 || users.saved[0].Username != "anna" {
		t.Errorf("saved users = %+v", users.saved)
	}
	if len(s.sent) != 1 || s.sent[0] != "Записал" {
		t.Errorf("sent = %q", s.sent)
	}
}

func TestAllowList(t *testing.T) {
	h, a, users, s := setup(1, 2)
	h.handle(context.Background(), s, privateMessage(42, "кофе 350"))
	if len(a.callers) != 0 || len(users.saved) != 0 {
		t.Error("stranger reached the agent")
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0], "42") {
		t.Errorf("sent = %q, want a refusal with the user's ID", s.sent)
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
