// Package telegram connects the agent to Telegram: it receives updates,
// identifies the family member and sends the agent's replies back.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const (
	maxInputRunes   = 1000 // longer messages are not expense notes; also caps token spend
	maxMessageRunes = 4096 // Telegram limit
	typingInterval  = 4 * time.Second
	sendAttempts    = 3
	sendTimeout     = 20 * time.Second

	replyHelp = "Я записываю расходы. Пишите обычным текстом:\n" +
		"• кофе 350\n" +
		"• такси 1200 вчера\n" +
		"• сколько потратил на еду в этом месяце\n" +
		"• покажи траты за неделю\n" +
		"• последнее было не 350, а 450\n" +
		"• удали такси"
	replyNotText   = "Я понимаю только текстовые сообщения. Например: «кофе 350»."
	replyTooLong   = "Слишком длинное сообщение. Напишите трату коротко, например: «кофе 350»."
	replyPrivate   = "Я работаю только в личных сообщениях — напишите мне напрямую."
	replyFailure   = "Что-то пошло не так, попробуйте ещё раз через минуту."
	replyForbidden = "Этот бот ведёт расходы одной семьи, и вас нет в списке. " +
		"Ваш Telegram ID: %d — передайте его главе семьи."
)

// Replier is the agent.
type Replier interface {
	Reply(ctx context.Context, caller agent.Caller, text string) (string, error)
	Reset(userID int64)
}

// UserSaver records family members.
type UserSaver interface {
	Upsert(ctx context.Context, u user.User) (user.User, error)
}

// Sender is the part of the Telegram API the handler uses; *bot.Bot implements it.
type Sender interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
	SendChatAction(ctx context.Context, params *bot.SendChatActionParams) (bool, error)
}

// Handler processes incoming Telegram updates.
type Handler struct {
	agent        Replier
	users        UserSaver
	allowed      map[int64]bool // empty: everyone is allowed
	replyTimeout time.Duration
	retryDelay   time.Duration // first pause between send attempts
	log          *slog.Logger

	// Messages of one user are handled one at a time, in order:
	// "кофе 350" followed by "нет, 450" must not race.
	userLocks sync.Map // int64 -> *sync.Mutex
}

// NewHandler creates a handler. allowedIDs restricts the bot to family members.
func NewHandler(a Replier, users UserSaver, allowedIDs []int64, replyTimeout time.Duration, log *slog.Logger) *Handler {
	allowed := make(map[int64]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = true
	}
	return &Handler{agent: a, users: users, allowed: allowed, replyTimeout: replyTimeout, retryDelay: time.Second, log: log}
}

// Handle is a bot.HandlerFunc.
func (h *Handler) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handle(ctx, b, update)
}

func (h *Handler) handle(ctx context.Context, s Sender, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.From == nil || msg.From.IsBot {
		return // edits, channel posts, service messages
	}
	from := msg.From
	chatID := msg.Chat.ID
	log := h.log.With("user_id", from.ID)

	if msg.Chat.Type != models.ChatTypePrivate {
		h.send(ctx, s, chatID, replyPrivate)
		return
	}
	if len(h.allowed) > 0 && !h.allowed[from.ID] {
		log.WarnContext(ctx, "message from a user outside the family", "username", from.Username)
		h.send(ctx, s, chatID, fmt.Sprintf(replyForbidden, from.ID))
		return
	}

	text := strings.TrimSpace(msg.Text)
	switch {
	case text == "":
		h.send(ctx, s, chatID, replyNotText)
		return
	case isCommand(text, "/start"), isCommand(text, "/help"):
		h.agent.Reset(from.ID)
		h.send(ctx, s, chatID, replyHelp)
		return
	case len([]rune(text)) > maxInputRunes:
		h.send(ctx, s, chatID, replyTooLong)
		return
	}

	lock := h.userLock(from.ID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(ctx, h.replyTimeout)
	defer cancel()

	// The panel lists members by name, and names change: refresh on every message.
	if _, err := h.users.Upsert(ctx, user.User{TelegramID: from.ID, FirstName: from.FirstName, Username: from.Username}); err != nil {
		log.ErrorContext(ctx, "save user", "err", err)
		h.send(ctx, s, chatID, replyFailure)
		return
	}

	stopTyping := h.keepTyping(ctx, s, chatID)
	reply, err := h.agent.Reply(ctx, agent.Caller{UserID: from.ID, Name: from.FirstName}, text)
	stopTyping()
	if err != nil {
		log.ErrorContext(ctx, "agent failed", "err", err)
		reply = replyFailure
	}
	h.send(ctx, s, chatID, reply)
}

// keepTyping shows "typing…" until the returned function is called:
// the indicator disappears after ~5 seconds, and the LLM can be slower.
func (h *Handler) keepTyping(ctx context.Context, s Sender, chatID int64) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(typingInterval)
		defer t.Stop()
		for {
			_, _ = s.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: chatID, Action: models.ChatActionTyping})
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (h *Handler) send(ctx context.Context, s Sender, chatID int64, text string) {
	if r := []rune(text); len(r) > maxMessageRunes {
		text = string(r[:maxMessageRunes-1]) + "…"
	}
	// Plain text: the model's output is not trusted to be valid Telegram markup.
	params := &bot.SendMessageParams{ChatID: chatID, Text: text}

	// The reply must go out even if the request context has just expired
	// (e.g. the LLM timed out and this is the error message).
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()

	// Retry network failures: silence is worse than a rare duplicate message.
	delay := h.retryDelay
	for attempt := 1; ; attempt++ {
		_, err := s.SendMessage(ctx, params)
		if err == nil {
			return
		}
		if attempt == sendAttempts {
			h.log.ErrorContext(ctx, "send message", "chat_id", chatID, "attempts", attempt, "err", err)
			return
		}
		h.log.WarnContext(ctx, "send message retry", "chat_id", chatID, "attempt", attempt, "err", err)
		select {
		case <-time.After(delay):
			delay *= 2
		case <-ctx.Done():
			h.log.ErrorContext(ctx, "send message", "chat_id", chatID, "err", ctx.Err())
			return
		}
	}
}

func (h *Handler) userLock(userID int64) *sync.Mutex {
	l, _ := h.userLocks.LoadOrStore(userID, &sync.Mutex{})
	return l.(*sync.Mutex)
}

// isCommand matches "/start" and "/start@MyBot".
func isCommand(text, cmd string) bool {
	first, _, _ := strings.Cut(text, " ")
	name, _, _ := strings.Cut(first, "@")
	return strings.EqualFold(name, cmd)
}
