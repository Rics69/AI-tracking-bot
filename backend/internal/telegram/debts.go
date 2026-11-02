package telegram

import (
	"context"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// DebtBook is the part of debts.Service the bot shows.
type DebtBook interface {
	List(ctx context.Context, userID int64) ([]debts.Debt, decimal.Decimal, error)
	NetWorth(ctx context.Context, userID int64, today time.Time) (debts.Snapshot, error)
}

func (h *Handler) showDebts(ctx context.Context, s Sender, chatID int64, u user.User) {
	if h.cfg.Debts == nil {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	list, total, err := h.cfg.Debts.List(ctx, u.TelegramID)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
		return
	}
	if len(list) == 0 {
		h.send(ctx, s, chatID, h.t.DebtsNone)
		return
	}
	worth, err := h.cfg.Debts.NetWorth(ctx, u.TelegramID, h.userDay(u))
	if err != nil {
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
		return
	}
	lines := make([]string, len(list))
	for i, d := range list {
		lines[i] = texts.Fill(h.t.DebtsLine, "name", d.Name, "balance", report.Money(d.Balance), "rate", d.Rate.String())
	}
	net := report.Money(worth.Net())
	h.send(ctx, s, chatID, texts.Fill(h.t.DebtsView, "net", net, "assets", report.Money(worth.Assets),
		"total", report.Money(total), "debts", strings.Join(lines, "\n")))
}
