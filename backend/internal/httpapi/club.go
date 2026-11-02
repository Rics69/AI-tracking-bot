package httpapi

import (
	"context"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/library"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func (s *Server) clubState(ctx context.Context, u user.User) (ClubState, error) {
	until, err := s.access.Until(ctx, u.TelegramID)
	if err != nil {
		return ClubState{}, err
	}
	return ClubState{Active: s.access.Has(ctx, u.TelegramID, access.FeatureLibrary), Until: until}, nil
}

func (s *Server) GetClub(ctx context.Context, _ GetClubRequestObject) (GetClubResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.clubState(ctx, u)
	if err != nil {
		return nil, err
	}
	return GetClub200JSONResponse(state), nil
}

func (s *Server) RedeemPromo(ctx context.Context, req RedeemPromoRequestObject) (RedeemPromoResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.access.Redeem(ctx, u.TelegramID, req.Body.Code); err != nil {
		return nil, err
	}
	s.events.Track(ctx, u.TelegramID, events.PromoRedeemed, nil)
	state, err := s.clubState(ctx, u)
	if err != nil {
		return nil, err
	}
	return RedeemPromo200JSONResponse(state), nil
}

func (s *Server) ListGuides(ctx context.Context, _ ListGuidesRequestObject) (ListGuidesResponseObject, error) {
	if _, err := consented(ctx); err != nil {
		return nil, err
	}
	list := s.library.List()
	items := make([]GuideInfo, len(list))
	for i, g := range list {
		items[i] = GuideInfo{Slug: g.Slug, Title: g.Title, Topic: g.Topic, Preview: g.Preview}
	}
	return ListGuides200JSONResponse{Items: items}, nil
}

// GetGuide opens a guide: all of it in the club, the first paragraph otherwise.
func (s *Server) GetGuide(ctx context.Context, req GetGuideRequestObject) (GetGuideResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	g, err := s.library.Get(req.Slug)
	if err != nil {
		return nil, err
	}
	open := s.access.Has(ctx, u.TelegramID, access.FeatureLibrary)
	s.events.Track(ctx, u.TelegramID, events.LibraryOpen, events.Props{"guide": g.Slug, "open": open})
	return GetGuide200JSONResponse(toAPIGuide(g, open)), nil
}

func toAPIGuide(g library.Guide, open bool) Guide {
	body := g.Preview
	if open {
		body = g.Body
	}
	return Guide{Slug: g.Slug, Title: g.Title, Topic: g.Topic, Body: body, Locked: !open}
}
