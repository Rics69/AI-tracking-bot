package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/card"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// cardPath serves the picture of a story. It takes no login: Telegram fetches
// the picture itself and cannot send init data, so the link is the pass.
const cardPath = basePath + "/card/"

const (
	cardLinkTTL = time.Hour

	cardGoal   = "goal"
	cardLevel  = "level"
	cardStreak = "streak"
)

var errBadCard = errors.New("bad card link")

func (s *Server) gameProfile(ctx context.Context, u user.User) (GameProfile, error) {
	day := s.today(u)
	p, err := s.game.Profile(ctx, u.TelegramID)
	if err != nil {
		return GameProfile{}, err
	}
	list, err := s.game.Quests(ctx, u.TelegramID, day)
	if err != nil {
		return GameProfile{}, err
	}
	quests := make([]Quest, len(list))
	for i, q := range list {
		quests[i] = Quest{Slug: q.Quest.Slug, Title: q.Quest.Title, Text: q.Quest.Text, Xp: q.Quest.XP,
			Manual: q.Quest.Check == game.CheckManual, Done: q.Status == game.StatusDone}
	}
	return GameProfile{Xp: p.XP, Level: p.Level, Streak: p.CurrentStreak(day), BestStreak: p.BestStreak, Quests: quests}, nil
}

func (s *Server) GetGame(ctx context.Context, _ GetGameRequestObject) (GetGameResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.gameProfile(ctx, u)
	if err != nil {
		return nil, err
	}
	return GetGame200JSONResponse(out), nil
}

func (s *Server) DoneQuest(ctx context.Context, req DoneQuestRequestObject) (DoneQuestResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	d, err := s.game.CompleteManual(ctx, u.TelegramID, req.Slug, s.today(u), s.family.Now())
	if err != nil {
		return nil, err
	}
	if d.Awarded {
		s.events.Track(ctx, u.TelegramID, events.QuestDone, events.Props{"quest": req.Slug})
	}
	out, err := s.gameProfile(ctx, u)
	if err != nil {
		return nil, err
	}
	return DoneQuest200JSONResponse(out), nil
}

func (s *Server) CreateCard(ctx context.Context, req CreateCardRequestObject) (CreateCardResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	kind, ref := string(req.Body.Kind), ""
	if kind == cardGoal {
		ref = strconv.FormatInt(deref(req.Body.GoalId), 10)
	}
	c, err := s.drawCard(ctx, u.TelegramID, kind, ref)
	if err != nil {
		return nil, err
	}
	token := s.signCard(u.TelegramID, kind, ref, s.family.Now().Add(cardLinkTTL))
	return CreateCard200JSONResponse{Url: cardPath + token, Text: strings.TrimSpace(c.Eyebrow + ": " + c.Value + " " + c.Title)}, nil
}

// drawCard builds a card of a user. It never asks for amounts: the link may
// be seen by anyone who gets hold of it.
func (s *Server) drawCard(ctx context.Context, userID int64, kind, ref string) (card.Card, error) {
	p, err := s.game.Profile(ctx, userID)
	if err != nil {
		return card.Card{}, err
	}
	var c card.Card
	switch kind {
	case cardGoal:
		id, err := strconv.ParseInt(ref, 10, 64)
		if err != nil {
			return card.Card{}, budget.ErrNotFound
		}
		g, err := s.budget.Goal(ctx, userID, id)
		if err != nil {
			return card.Card{}, err
		}
		c = report.GoalCard(s.texts, g, false)
	case cardLevel:
		c = report.LevelCard(s.texts, p.Level)
	case cardStreak:
		c = report.StreakCard(s.texts, p.Streak)
	default:
		return card.Card{}, errBadCard
	}
	if name := s.botName(ctx); name != "" {
		if code, err := s.game.ReferralCode(ctx, userID); err == nil {
			c.Footer = "t.me/" + name + "?start=ref_" + code
		}
	}
	return c, nil
}

func (s *Server) cardMAC(payload string) string {
	mac := hmac.New(sha256.New, []byte("card:"+s.cardSecret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) signCard(userID int64, kind, ref string, expires time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d:%s:%s:%d", userID, kind, ref, expires.Unix()))
	return payload + "." + s.cardMAC(payload)
}

// serveCard renders the picture of a signed link.
func (s *Server) serveCard(w http.ResponseWriter, r *http.Request) {
	data, err := s.cardFromToken(r.Context(), strings.TrimPrefix(r.URL.Path, cardPath))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

func (s *Server) cardFromToken(ctx context.Context, token string) ([]byte, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.cardMAC(payload))) {
		return nil, errBadCard
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, errBadCard
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 4 {
		return nil, errBadCard
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, errBadCard
	}
	expires, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || s.family.Now().Unix() > expires {
		return nil, errBadCard
	}
	c, err := s.drawCard(ctx, userID, parts[1], parts[2])
	if err != nil {
		return nil, err
	}
	return card.Render(c)
}
