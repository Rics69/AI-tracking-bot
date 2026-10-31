package telegram

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/quick"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// Quick records plain messages without the LLM; *quick.Service implements it.
type Quick interface {
	Record(ctx context.Context, userID int64, text string, today time.Time) (ledger.Operation, bool, error)
	Delete(ctx context.Context, userID, id int64) error
}

// recordQuick reports whether the message was recorded without the agent. It
// does not depend on limits or on the LLM: "кофе 350" always works.
func (h *Handler) recordQuick(ctx context.Context, s Sender, chatID int64, u user.User, text string, day time.Time) bool {
	if h.cfg.Quick == nil {
		return false
	}
	op, ok, err := h.cfg.Quick.Record(ctx, u.TelegramID, text, day)
	if err != nil {
		h.log.ErrorContext(ctx, "quick record", "user_id", u.TelegramID, "err", err) // the agent may still manage
		return false
	}
	if !ok {
		return false
	}
	name := events.ExpenseAdded
	if op.Kind() == ledger.KindIncome {
		name = events.IncomeAdded
	}
	h.cfg.Events.Track(ctx, u.TelegramID, name, events.Props{"via": events.ViaFast})
	reply := quick.Reply(h.t, op, day)
	h.agent.Remember(ctx, u.TelegramID, text, reply)
	h.reply(ctx, s, chatID, 0, view{
		text:    reply,
		buttons: [][]models.InlineKeyboardButton{row(button(h.t.QuickFixButton, cbFix+":"+strconv.FormatInt(op.ID, 10)))},
	})
	return true
}

// quickFix explains how to correct a record: words go to the agent, which
// knows the record from the conversation.
func (h *Handler) quickFix(ctx context.Context, s Sender, chatID int64, id int64) {
	h.reply(ctx, s, chatID, 0, view{
		text:    h.t.QuickFixHint,
		buttons: [][]models.InlineKeyboardButton{row(button(h.t.QuickDeleteButton, cbFixDelete+":"+strconv.FormatInt(id, 10)))},
	})
}

func (h *Handler) quickDelete(ctx context.Context, s Sender, chatID int64, messageID int, u user.User, id int64) {
	if h.cfg.Quick == nil {
		return
	}
	err := h.cfg.Quick.Delete(ctx, u.TelegramID, id)
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		h.reply(ctx, s, chatID, messageID, view{text: h.t.QuickGone})
	case err != nil:
		h.replyOrFail(ctx, s, chatID, messageID, view{}, err)
	default:
		h.reply(ctx, s, chatID, messageID, view{text: h.t.QuickDeleted})
	}
}
