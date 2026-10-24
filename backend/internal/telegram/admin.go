package telegram

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/stats"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// Commands of the owner (ADMIN_TELEGRAM_ID). For everyone else they are
// unknown commands and get the help text.

func (h *Handler) isAdmin(u user.User) bool {
	return h.cfg.AdminID != 0 && u.TelegramID == h.cfg.AdminID
}

func (h *Handler) showStats(ctx context.Context, s Sender, chatID int64, u user.User) {
	if h.cfg.Stats == nil {
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	now := h.now().In(u.Location(h.cfg.Location))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	snap, err := h.cfg.Stats.Snapshot(ctx, now, today, monthStart)
	if err != nil {
		h.log.ErrorContext(ctx, "stats", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	h.send(ctx, s, chatID, stats.Render(snap))
}

// ban handles "/ban <id>" and "/unban <id>".
func (h *Handler) ban(ctx context.Context, s Sender, chatID int64, admin user.User, text string) {
	arg, on := commandArg(text, "/ban")
	if !on {
		arg, _ = commandArg(text, "/unban")
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	switch {
	case err != nil || id <= 0:
		h.send(ctx, s, chatID, h.t.AdminBanUsage)
		return
	case id == admin.TelegramID:
		h.send(ctx, s, chatID, h.t.AdminSelf)
		return
	}
	idText := strconv.FormatInt(id, 10)
	_, err = h.family.SetBlocked(ctx, id, on)
	switch {
	case errors.Is(err, user.ErrNotFound):
		h.send(ctx, s, chatID, texts.Fill(h.t.AdminUnknownUser, "id", idText))
	case err != nil:
		h.log.ErrorContext(ctx, "set blocked", "target_id", id, "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
	case on:
		h.log.WarnContext(ctx, "user blocked by the owner", "target_id", id)
		h.send(ctx, s, chatID, texts.Fill(h.t.AdminBanned, "id", idText))
	default:
		h.log.InfoContext(ctx, "user unblocked by the owner", "target_id", id)
		h.send(ctx, s, chatID, texts.Fill(h.t.AdminUnbanned, "id", idText))
	}
}

// AdminCommands is the command menu of the owner's chat: everyone's commands
// and the owner's own.
func AdminCommands(t texts.Bot, withApp bool) []models.BotCommand {
	return append(Commands(t, withApp),
		models.BotCommand{Command: "stats", Description: t.CommandStats},
		models.BotCommand{Command: "ban", Description: t.CommandBan},
		models.BotCommand{Command: "unban", Description: t.CommandUnban},
	)
}
