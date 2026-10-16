// Package telegram connects the agent to Telegram: it receives updates,
// identifies the family member and sends the agent's replies back.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
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
	replyForbidden = "Этот бот ведёт расходы одной семьи. Чтобы присоединиться, " +
		"попросите у главы семьи ссылку-приглашение. Ваш Telegram ID: %d."
	replyJoined        = "%s, добро пожаловать в семью!\n\n" + replyHelp
	replyAlreadyMember = "Вы уже в семье.\n\n" + replyHelp
	replyInviteInvalid = "Это приглашение недействительно: оно истекло, отозвано или уже использовано. " +
		"Попросите у главы семьи новую ссылку."
)

// Replier is the agent.
type Replier interface {
	Reply(ctx context.Context, caller agent.Caller, text string) (string, error)
	Reset(userID int64)
}

// Family decides who may talk to the bot; *family.Service implements it.
type Family interface {
	Admit(ctx context.Context, tg user.User) (user.User, error)
	Join(ctx context.Context, code string, tg user.User) (member user.User, joined bool, err error)
}

// Sender is the part of the Telegram API the handler uses; *bot.Bot implements it.
type Sender interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
	SendChatAction(ctx context.Context, params *bot.SendChatActionParams) (bool, error)
}

// Handler processes incoming Telegram updates.
type Handler struct {
	agent        Replier
	family       Family
	replyTimeout time.Duration
	retryDelay   time.Duration // first pause between send attempts
	log          *slog.Logger

	// Messages of one user are handled one at a time, in order:
	// "кофе 350" followed by "нет, 450" must not race.
	userLocks sync.Map // int64 -> *sync.Mutex
}

// NewHandler creates a handler.
func NewHandler(a Replier, f Family, replyTimeout time.Duration, log *slog.Logger) *Handler {
	return &Handler{agent: a, family: f, replyTimeout: replyTimeout, retryDelay: time.Second, log: log}
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
		countMessage(msgIgnored)
		h.send(ctx, s, chatID, replyPrivate)
		return
	}
	tg := user.User{TelegramID: from.ID, FirstName: from.FirstName, Username: from.Username}
	text := strings.TrimSpace(msg.Text)

	lock := h.userLock(from.ID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(ctx, h.replyTimeout)
	defer cancel()

	// An invite link arrives as "/start <code>"; it is the only way in for strangers.
	if code, ok := commandArg(text, "/start"); ok && code != "" {
		h.join(ctx, s, chatID, code, tg)
		return
	}

	// Membership is checked on every message: a member removed in the panel
	// loses access immediately. Admit also refreshes the name the panel shows.
	if _, err := h.family.Admit(ctx, tg); err != nil {
		if errors.Is(err, family.ErrNotMember) {
			countMessage(msgRefused)
			log.WarnContext(ctx, "message from a user outside the family", "username", from.Username)
			h.send(ctx, s, chatID, fmt.Sprintf(replyForbidden, from.ID))
			return
		}
		countMessage(msgFailure)
		log.ErrorContext(ctx, "check membership", "err", err)
		h.send(ctx, s, chatID, replyFailure)
		return
	}

	switch {
	case text == "":
		countMessage(msgIgnored)
		h.send(ctx, s, chatID, replyNotText)
		return
	case isCommand(text, "/start"), isCommand(text, "/help"):
		countMessage(msgHelp)
		h.agent.Reset(from.ID)
		h.send(ctx, s, chatID, replyHelp)
		return
	case len([]rune(text)) > maxInputRunes:
		countMessage(msgIgnored)
		h.send(ctx, s, chatID, replyTooLong)
		return
	}

	stopTyping := h.keepTyping(ctx, s, chatID)
	reply, err := h.agent.Reply(ctx, agent.Caller{UserID: from.ID, Name: from.FirstName}, text)
	stopTyping()
	if err != nil {
		countMessage(msgAgentFailed)
		log.ErrorContext(ctx, "agent failed", "err", err)
		reply = replyFailure
	} else {
		countMessage(msgAnswered)
	}
	h.send(ctx, s, chatID, reply)
}

func (h *Handler) join(ctx context.Context, s Sender, chatID int64, code string, tg user.User) {
	member, joined, err := h.family.Join(ctx, code, tg)
	switch {
	case errors.Is(err, family.ErrInviteInvalid):
		countMessage(msgInviteInvalid)
		h.log.WarnContext(ctx, "invalid invite", "user_id", tg.TelegramID)
		h.send(ctx, s, chatID, replyInviteInvalid)
	case err != nil:
		countMessage(msgFailure)
		h.log.ErrorContext(ctx, "join family", "user_id", tg.TelegramID, "err", err)
		h.send(ctx, s, chatID, replyFailure)
	case !joined:
		countMessage(msgAlreadyMember)
		h.send(ctx, s, chatID, replyAlreadyMember)
	default:
		countMessage(msgJoined)
		h.log.InfoContext(ctx, "joined the family", "user_id", tg.TelegramID, "username", tg.Username)
		h.send(ctx, s, chatID, fmt.Sprintf(replyJoined, member.FirstName))
	}
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
			sendFailuresTotal.Inc()
			h.log.ErrorContext(ctx, "send message", "chat_id", chatID, "attempts", attempt, "err", err)
			return
		}
		sendRetriesTotal.Inc()
		h.log.WarnContext(ctx, "send message retry", "chat_id", chatID, "attempt", attempt, "err", err)
		select {
		case <-time.After(delay):
			delay *= 2
		case <-ctx.Done():
			sendFailuresTotal.Inc()
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
	_, ok := commandArg(text, cmd)
	return ok
}

// commandArg returns the argument of a command: "/start abc" → "abc", true.
func commandArg(text, cmd string) (string, bool) {
	first, rest, _ := strings.Cut(text, " ")
	name, _, _ := strings.Cut(first, "@")
	if !strings.EqualFold(name, cmd) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}
