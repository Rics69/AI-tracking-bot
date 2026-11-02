package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/usage"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// inviteLife is how long a personal link to the club's chats works.
const inviteLife = 24 * time.Hour

// Access is the part of access.Service the bot needs.
type Access interface {
	Has(ctx context.Context, userID int64, feature string) bool
	Until(ctx context.Context, userID int64) (*time.Time, error)
	Grant(ctx context.Context, userID int64, source string, days int) (time.Time, error)
	Revoke(ctx context.Context, userID int64) error
	Redeem(ctx context.Context, userID int64, code string) (time.Time, error)
	NewPromo(ctx context.Context, code string, days, maxUses int) (string, error)
	Members(ctx context.Context) (int, error)
	TierLimits(ctx context.Context, tier string) (usage.Limits, error)
	SetLimits(ctx context.Context, tier string, l usage.Limits) error
}

func (h *Handler) clubDate(t time.Time, u user.User) string {
	loc := u.Location(h.cfg.Location)
	y, m, d := t.In(loc).Date()
	return agent.HumanDate(time.Date(y, m, d, 0, 0, 0, 0, time.UTC), h.userDay(u))
}

func (h *Handler) showClub(ctx context.Context, s Sender, chatID int64, u user.User) {
	if h.cfg.Access == nil {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	until, err := h.cfg.Access.Until(ctx, u.TelegramID)
	if err != nil {
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
		return
	}
	if !h.cfg.Access.Has(ctx, u.TelegramID, access.FeatureLibrary) {
		h.send(ctx, s, chatID, h.t.ClubAbout)
		return
	}
	v := view{text: texts.Fill(h.t.ClubActive, "date", h.clubDate(*until, u))}
	if len(h.cfg.ClubChats) > 0 {
		v.buttons = [][]models.InlineKeyboardButton{row(button(h.t.ClubChatButton, cbClubJoin))}
	}
	h.reply(ctx, s, chatID, 0, v)
}

// clubJoin gives personal one-time links: entitlements decide who is in, and a
// link passed on works for one person and for a day.
func (h *Handler) clubJoin(ctx context.Context, s Sender, chatID int64, u user.User) {
	if h.cfg.Access == nil || !h.cfg.Access.Has(ctx, u.TelegramID, access.FeatureClubChat) {
		h.send(ctx, s, chatID, h.t.ClubAbout)
		return
	}
	expires := int(h.now().Add(inviteLife).Unix())
	var links []string
	for _, chat := range h.cfg.ClubChats {
		link, err := s.CreateChatInviteLink(ctx, &bot.CreateChatInviteLinkParams{ChatID: chat, ExpireDate: expires, MemberLimit: 1})
		if err != nil {
			h.log.ErrorContext(ctx, "club invite link", "chat_id", chat, "err", err)
			h.send(ctx, s, chatID, h.t.Failure)
			return
		}
		links = append(links, link.InviteLink)
	}
	h.send(ctx, s, chatID, texts.Fill(h.t.ClubLinks, "links", strings.Join(links, "\n")))
}

// RemoveFromClub takes a user out of the club's chats. Banning and unbanning
// at once removes them and leaves the door open for a return. Someone who is
// not in a chat is not a problem.
func (h *Handler) RemoveFromClub(ctx context.Context, s Sender, userID int64) error {
	for _, chat := range h.cfg.ClubChats {
		if _, err := s.BanChatMember(ctx, &bot.BanChatMemberParams{ChatID: chat, UserID: userID}); err != nil {
			if permanent(err) {
				h.log.WarnContext(ctx, "remove from club chat", "chat_id", chat, "user_id", userID, "err", err)
				continue
			}
			return err
		}
		if _, err := s.UnbanChatMember(ctx, &bot.UnbanChatMemberParams{ChatID: chat, UserID: userID, OnlyIfBanned: true}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) redeemPromo(ctx context.Context, s Sender, chatID int64, u user.User, code string) {
	if h.cfg.Access == nil {
		h.send(ctx, s, chatID, h.t.Help)
		return
	}
	if code == "" {
		h.send(ctx, s, chatID, h.t.PromoUsage)
		return
	}
	end, err := h.cfg.Access.Redeem(ctx, u.TelegramID, code)
	switch {
	case errors.Is(err, access.ErrPromoInvalid):
		h.send(ctx, s, chatID, h.t.PromoInvalid)
	case errors.Is(err, access.ErrPromoUsed):
		h.send(ctx, s, chatID, h.t.PromoUsed)
	case err != nil:
		h.replyOrFail(ctx, s, chatID, 0, view{}, err)
	default:
		h.cfg.Events.Track(ctx, u.TelegramID, events.PromoRedeemed, nil)
		h.send(ctx, s, chatID, texts.Fill(h.t.PromoDone, "date", h.clubDate(end, u)))
	}
}

// Owner.

func parseIDs(arg string) ([]int64, bool) {
	fields := strings.Fields(arg)
	out := make([]int64, len(fields))
	for i, f := range fields {
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil || n <= 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, len(out) > 0
}

// grant handles "/grant <id> <days>".
func (h *Handler) grant(ctx context.Context, s Sender, chatID int64, arg string) {
	n, ok := parseIDs(arg)
	if !ok || len(n) != 2 || h.cfg.Access == nil {
		h.send(ctx, s, chatID, h.t.AdminGrantUsage)
		return
	}
	id, days := n[0], int(n[1])
	if _, err := h.family.Get(ctx, id); errors.Is(err, user.ErrNotFound) {
		h.send(ctx, s, chatID, texts.Fill(h.t.AdminUnknownUser, "id", strconv.FormatInt(id, 10)))
		return
	}
	end, err := h.cfg.Access.Grant(ctx, id, access.SourceAdmin, days)
	if err != nil {
		h.log.ErrorContext(ctx, "grant club", "target_id", id, "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	h.send(ctx, s, chatID, texts.Fill(h.t.AdminGranted, "id", strconv.FormatInt(id, 10), "date", end.Format(time.DateOnly)))
	h.notify(ctx, s, id, h.t.ClubGranted)
}

// revoke handles "/revoke <id>": the club and the chats go at once.
func (h *Handler) revoke(ctx context.Context, s Sender, chatID int64, arg string) {
	n, ok := parseIDs(arg)
	if !ok || len(n) != 1 || h.cfg.Access == nil {
		h.send(ctx, s, chatID, h.t.AdminRevokeUsage)
		return
	}
	id := n[0]
	if err := h.cfg.Access.Revoke(ctx, id); err != nil {
		h.log.ErrorContext(ctx, "revoke club", "target_id", id, "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	if err := h.RemoveFromClub(ctx, s, id); err != nil {
		h.log.WarnContext(ctx, "remove from club", "target_id", id, "err", err)
	}
	h.send(ctx, s, chatID, texts.Fill(h.t.AdminRevoked, "id", strconv.FormatInt(id, 10)))
}

// newPromo handles "/newpromo <days> <uses> [CODE]".
func (h *Handler) newPromo(ctx context.Context, s Sender, chatID int64, arg string) {
	fields := strings.Fields(arg)
	if len(fields) < 2 || len(fields) > 3 || h.cfg.Access == nil {
		h.send(ctx, s, chatID, h.t.AdminPromoUsage)
		return
	}
	n, ok := parseIDs(fields[0] + " " + fields[1])
	if !ok {
		h.send(ctx, s, chatID, h.t.AdminPromoUsage)
		return
	}
	code := ""
	if len(fields) == 3 {
		code = fields[2]
	}
	code, err := h.cfg.Access.NewPromo(ctx, code, int(n[0]), int(n[1]))
	if err != nil {
		h.log.ErrorContext(ctx, "new promo", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	h.send(ctx, s, chatID, texts.Fill(h.t.AdminPromo, "code", code, "days", fields[0], "uses", fields[1]))
}

// limits handles "/limits" and "/limits <free|club> <messages> <tokens>".
func (h *Handler) limits(ctx context.Context, s Sender, chatID int64, arg string) {
	if h.cfg.Access == nil {
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	fields := strings.Fields(arg)
	if len(fields) == 3 && (fields[0] == access.TierFree || fields[0] == access.TierClub) {
		messages, errM := strconv.Atoi(fields[1])
		tokens, errT := strconv.ParseInt(fields[2], 10, 64)
		if errM != nil || errT != nil || messages < 0 || tokens < 0 {
			h.send(ctx, s, chatID, h.t.AdminLimitsUsage)
			return
		}
		if err := h.cfg.Access.SetLimits(ctx, fields[0], usage.Limits{Messages: messages, Tokens: tokens}); err != nil {
			h.log.ErrorContext(ctx, "set limits", "err", err)
			h.send(ctx, s, chatID, h.t.Failure)
			return
		}
	} else if len(fields) != 0 {
		h.send(ctx, s, chatID, h.t.AdminLimitsUsage)
		return
	}
	free, errF := h.cfg.Access.TierLimits(ctx, access.TierFree)
	club, errC := h.cfg.Access.TierLimits(ctx, access.TierClub)
	if err := errors.Join(errF, errC); err != nil {
		h.log.ErrorContext(ctx, "read limits", "err", err)
		h.send(ctx, s, chatID, h.t.Failure)
		return
	}
	h.send(ctx, s, chatID, texts.Fill(h.t.AdminLimits,
		"free_messages", strconv.Itoa(free.Messages), "free_tokens", strconv.FormatInt(free.Tokens, 10),
		"club_messages", strconv.Itoa(club.Messages), "club_tokens", strconv.FormatInt(club.Tokens, 10)))
}
