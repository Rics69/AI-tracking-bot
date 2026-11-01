package telegram

import (
	"context"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/startparam"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// landing reads a start parameter. A label that is a live invite code is an
// invite: codes are random and may begin like a label.
func (h *Handler) landing(ctx context.Context, raw string) startparam.Param {
	p := startparam.Parse(raw)
	if p.Labelled() {
		if _, err := h.family.Invitation(ctx, raw); err == nil {
			return p.AsInvite()
		}
	}
	return p
}

// holdStart keeps the parameter of a link until the data policy is accepted.
// Nothing about the person goes to the events yet, only that someone came.
func (h *Handler) holdStart(ctx context.Context, u user.User, raw string) {
	p := h.landing(ctx, raw)
	if p.Kind != startparam.Plain {
		if _, err := h.family.SetPendingStart(ctx, u.TelegramID, raw); err != nil {
			h.log.ErrorContext(ctx, "keep start parameter", "user_id", u.TelegramID, "err", err)
		}
	}
	h.cfg.Events.Track(ctx, 0, events.Start, startProps(p))
}

// arrive records where a user who accepted the policy came from. A plain
// /start says nothing new about someone who has a source already.
func (h *Handler) arrive(ctx context.Context, u user.User, p startparam.Param, event string) {
	if u.PendingStart != "" {
		if _, err := h.family.SetPendingStart(ctx, u.TelegramID, ""); err != nil {
			h.log.ErrorContext(ctx, "clear start parameter", "user_id", u.TelegramID, "err", err)
		}
	}
	if p.Kind != startparam.Plain || u.FirstSource == "" {
		if _, err := h.family.SetSource(ctx, u.TelegramID, p.Source); err != nil {
			h.log.ErrorContext(ctx, "save source", "user_id", u.TelegramID, "err", err)
		}
	}
	h.cfg.Events.Track(ctx, u.TelegramID, event, startProps(p))
	if event == events.Consented {
		h.landOnReferral(ctx, u, p)
	}
}

// start answers /start of a user who has accepted the policy: a link leads
// where it says, a plain /start goes back to the dialog left unfinished.
func (h *Handler) start(ctx context.Context, s Sender, chatID int64, u user.User, raw string) {
	if raw == "" {
		raw = u.PendingStart // the policy was accepted in the mini app
	}
	p := h.landing(ctx, raw)
	h.arrive(ctx, u, p, events.Start)
	if p.Kind == startparam.Invite {
		h.offerInvitation(ctx, s, chatID, p.Raw)
		return
	}
	h.agent.Reset(ctx, u.TelegramID)
	if p.Kind == startparam.Plain && h.resume(ctx, s, chatID, u) {
		return
	}
	if h.route(ctx, s, chatID, u, p, false) {
		return
	}
	h.send(ctx, s, chatID, h.t.Welcome)
}

func startProps(p startparam.Param) events.Props {
	props := events.Props{"source": p.Source}
	if p.Feature != "" {
		props["feature"] = p.Feature
	}
	if p.Ref != "" {
		props["ref"] = p.Ref
	}
	return props
}
