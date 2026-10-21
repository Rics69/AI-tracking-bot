// Package telegram connects the agent to Telegram: it receives updates,
// identifies the user, runs commands and sends the agent's replies back.
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
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

const (
	maxInputRunes   = 1000 // longer messages are not expense notes; also caps token spend
	maxMessageRunes = 4096 // Telegram limit
	typingInterval  = 4 * time.Second
	sendAttempts    = 3
	sendTimeout     = 20 * time.Second
)

// ErrUndeliverable means retrying will not help: the chat does not exist,
// the user blocked the bot, or the request is invalid.
var ErrUndeliverable = errors.New("telegram message cannot be delivered")

// permanent reports Telegram errors that a retry cannot fix.
func permanent(err error) bool {
	return errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorForbidden) ||
		errors.Is(err, bot.ErrorUnauthorized) || errors.Is(err, bot.ErrorNotFound)
}

// Replier is the agent.
type Replier interface {
	Reply(ctx context.Context, caller agent.Caller, text string) (string, error)
	Reset(ctx context.Context, userID int64)
}

// Family manages users and families; *family.Service implements it.
type Family interface {
	Admit(ctx context.Context, tg user.User) (user.User, error)
	Get(ctx context.Context, telegramID int64) (user.User, error)
	Consent(ctx context.Context, telegramID int64) (user.User, error)
	Invitation(ctx context.Context, code string) (head user.User, err error)
	Join(ctx context.Context, code string, telegramID int64) (user.User, error)
	Leave(ctx context.Context, telegramID int64) (user.User, error)
	Remove(ctx context.Context, head user.User, telegramID int64) error
	Members(ctx context.Context, familyID int64) ([]user.User, error)
	CreateInvite(ctx context.Context, head user.User) (family.CreatedInvite, error)
	SetWeeklyReport(ctx context.Context, by user.User, telegramID int64, on bool) (user.User, error)
	SetTimezone(ctx context.Context, telegramID int64, tz string) (user.User, error)
	DeleteAccount(ctx context.Context, telegramID int64) error
}

// Meter enforces per-user daily limits; *usage.Meter implements it.
type Meter interface {
	Allow(ctx context.Context, userID int64, day time.Time) (bool, error)
}

// Sender is the part of the Telegram API the handler uses; *bot.Bot implements it.
type Sender interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
	SendChatAction(ctx context.Context, params *bot.SendChatActionParams) (bool, error)
	EditMessageText(ctx context.Context, params *bot.EditMessageTextParams) (*models.Message, error)
	AnswerCallbackQuery(ctx context.Context, params *bot.AnswerCallbackQueryParams) (bool, error)
}

// Config tunes the handler.
type Config struct {
	ReplyTimeout time.Duration
	Location     *time.Location // timezone of users who have not chosen one
	AdminID      int64          // receives /feedback; 0: feedback is only logged
	PrivacyURL   string         // full data policy; empty: /privacy shows the summary only
	Texts        texts.Bot
}

// Handler processes incoming Telegram updates.
type Handler struct {
	agent      Replier
	family     Family
	meter      Meter
	cfg        Config
	t          texts.Bot
	retryDelay time.Duration // first pause between send attempts
	now        func() time.Time
	log        *slog.Logger

	// Updates of one user are handled one at a time, in order:
	// "кофе 350" followed by "нет, 450" must not race.
	userLocks sync.Map // int64 -> *sync.Mutex
}

// NewHandler creates a handler.
func NewHandler(a Replier, f Family, m Meter, cfg Config, log *slog.Logger) *Handler {
	return &Handler{
		agent: a, family: f, meter: m, cfg: cfg, t: cfg.Texts,
		retryDelay: time.Second, now: time.Now, log: log,
	}
}

// Handle is a bot.HandlerFunc.
func (h *Handler) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handle(ctx, b, update)
}

func (h *Handler) handle(ctx context.Context, s Sender, update *models.Update) {
	switch {
	case update.CallbackQuery != nil:
		h.handleCallback(ctx, s, update.CallbackQuery)
	case update.Message != nil:
		h.handleMessage(ctx, s, update.Message)
	}
	// Edits, channel posts and the like are ignored.
}

func (h *Handler) handleMessage(ctx context.Context, s Sender, msg *models.Message) {
	if msg.From == nil || msg.From.IsBot {
		return // service messages
	}
	from := msg.From
	chatID := msg.Chat.ID
	if msg.Chat.Type != models.ChatTypePrivate {
		h.send(ctx, s, chatID, h.t.Private)
		return
	}
	text := strings.TrimSpace(msg.Text)

	unlock := h.lock(from.ID)
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, h.cfg.ReplyTimeout)
	defer cancel()

	u, ok := h.admit(ctx, s, chatID, from)
	if !ok {
		return
	}
	log := h.log.With("user_id", u.TelegramID)

	// The consent message points here, so the policy is readable before consent.
	if isCommand(text, "/privacy") {
		h.send(ctx, s, chatID, h.privacyText())
		return
	}
	// An invite link arrives as "/start <code>".
	code, _ := commandArg(text, "/start")
	if !u.Consented() {
		h.askConsent(ctx, s, chatID, code)
		return
	}
	if code != "" {
		h.offerInvitation(ctx, s, chatID, code)
		return
	}

	switch {
	case text == "":
		h.send(ctx, s, chatID, h.t.NotText)
		return
	case isCommand(text, "/start"):
		h.agent.Reset(ctx, u.TelegramID)
		h.send(ctx, s, chatID, h.t.Welcome)
		return
	case isCommand(text, "/help"):
		h.agent.Reset(ctx, u.TelegramID)
		h.send(ctx, s, chatID, h.t.Help)
		return
	case isCommand(text, "/family"):
		h.showFamily(ctx, s, chatID, u)
		return
	case isCommand(text, "/settings"):
		h.reply(ctx, s, chatID, 0, h.settingsView(u))
		return
	case isCommand(text, "/feedback"):
		arg, _ := commandArg(text, "/feedback")
		h.feedback(ctx, s, chatID, u, arg)
		return
	case isCommand(text, "/delete_me"):
		h.confirmDelete(ctx, s, chatID, u)
		return
	case strings.HasPrefix(text, "/"):
		h.send(ctx, s, chatID, h.t.Help) // an unknown command is not an expense
		return
	case len([]rune(text)) > maxInputRunes:
		h.send(ctx, s, chatID, h.t.TooLong)
		return
	}

	loc := u.Location(h.cfg.Location)
	allowed, err := h.meter.Allow(ctx, u.TelegramID, today(h.now(), loc))
	if err != nil {
		log.ErrorContext(ctx, "check usage limit", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	if !allowed {
		log.WarnContext(ctx, "daily limit reached")
		h.send(ctx, s, chatID, h.t.LimitReached)
		return
	}

	stopTyping := h.keepTyping(ctx, s, chatID)
	reply, err := h.agent.Reply(ctx, agent.Caller{UserID: u.TelegramID, Name: u.FirstName, Location: loc}, text)
	stopTyping()
	if err != nil {
		log.ErrorContext(ctx, "agent failed", "err", err)
		reply = h.t.Failure
	}
	h.send(ctx, s, chatID, reply)
}

// admit identifies the sender, creating them on the first message. It
// answers the user itself when they cannot be served.
func (h *Handler) admit(ctx context.Context, s Sender, chatID int64, from *models.User) (user.User, bool) {
	u, err := h.family.Admit(ctx, user.User{TelegramID: from.ID, FirstName: from.FirstName, Username: from.Username})
	switch {
	case errors.Is(err, family.ErrBlocked):
		h.log.WarnContext(ctx, "blocked user", "user_id", from.ID)
		h.send(ctx, s, chatID, h.t.Blocked)
		return user.User{}, false
	case err != nil:
		h.log.ErrorContext(ctx, "admit user", "user_id", from.ID, "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return user.User{}, false
	}
	return u, true
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
	_ = h.Send(ctx, s, chatID, text) // logged inside
}

// Send delivers a plain-text message, retrying network failures. It is used
// for replies and for messages the bot starts itself (weekly reports).
func (h *Handler) Send(ctx context.Context, s Sender, chatID int64, text string) error {
	return h.sendMessage(ctx, s, &bot.SendMessageParams{ChatID: chatID, Text: text})
}

func (h *Handler) sendMessage(ctx context.Context, s Sender, params *bot.SendMessageParams) error {
	if r := []rune(params.Text); len(r) > maxMessageRunes {
		params.Text = string(r[:maxMessageRunes-1]) + "…"
	}
	// Plain text, no parse mode: the model's output is not trusted to be valid markup.
	chatID := params.ChatID

	// The reply must go out even if the request context has just expired
	// (e.g. the LLM timed out and this is the error message).
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()

	// Retry network failures: silence is worse than a rare duplicate message.
	delay := h.retryDelay
	for attempt := 1; ; attempt++ {
		_, err := s.SendMessage(ctx, params)
		if err == nil {
			return nil
		}
		if permanent(err) {
			h.log.WarnContext(ctx, "message undeliverable", "chat_id", chatID, "err", err)
			return fmt.Errorf("%w: %w", ErrUndeliverable, err)
		}
		if attempt == sendAttempts {
			h.log.ErrorContext(ctx, "send message", "chat_id", chatID, "attempts", attempt, "err", err)
			return err
		}
		h.log.WarnContext(ctx, "send message retry", "chat_id", chatID, "attempt", attempt, "err", err)
		select {
		case <-time.After(delay):
			delay *= 2
		case <-ctx.Done():
			h.log.ErrorContext(ctx, "send message", "chat_id", chatID, "err", ctx.Err())
			return ctx.Err()
		}
	}
}

func (h *Handler) lock(userID int64) (unlock func()) {
	m, _ := h.userLocks.LoadOrStore(userID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// today is the calendar date at now in loc, as midnight UTC like expense dates.
func today(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
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
