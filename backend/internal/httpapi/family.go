package httpapi

import (
	"context"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
)

func (s *Server) RemoveUser(ctx context.Context, req RemoveUserRequestObject) (RemoveUserResponseObject, error) {
	head, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.family.Remove(ctx, head, req.TelegramId); err != nil {
		return nil, err
	}
	return RemoveUser204Response{}, nil
}

func (s *Server) UpdateUser(ctx context.Context, req UpdateUserRequestObject) (UpdateUserResponseObject, error) {
	head, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.family.SetWeeklyReport(ctx, head, req.TelegramId, req.Body.WeeklyReport)
	if err != nil {
		return nil, err
	}
	return UpdateUser200JSONResponse(toAPIUser(u)), nil
}

func (s *Server) ListInvites(ctx context.Context, _ ListInvitesRequestObject) (ListInvitesResponseObject, error) {
	head, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	invites, err := s.family.ListInvites(ctx, head.FamilyID)
	if err != nil {
		return nil, err
	}
	now := s.family.Now()
	items := make([]Invite, len(invites))
	for i, inv := range invites {
		items[i] = Invite{
			Id:        inv.ID,
			Status:    InviteStatus(inv.Status(now)),
			CreatedAt: inv.CreatedAt,
			ExpiresAt: inv.ExpiresAt,
			UsedAt:    inv.UsedAt,
			UsedBy:    inv.UsedBy,
		}
	}
	return ListInvites200JSONResponse{Items: items}, nil
}

func (s *Server) CreateInvite(ctx context.Context, _ CreateInviteRequestObject) (CreateInviteResponseObject, error) {
	head, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := s.family.CreateInvite(ctx, head)
	if err != nil {
		return nil, err
	}
	return CreateInvite201JSONResponse{
		Id:        inv.ID,
		Status:    InviteStatus(family.InviteActive),
		CreatedAt: inv.CreatedAt,
		ExpiresAt: inv.ExpiresAt,
		Link:      inv.Link,
	}, nil
}

func (s *Server) RevokeInvite(ctx context.Context, req RevokeInviteRequestObject) (RevokeInviteResponseObject, error) {
	head, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.family.RevokeInvite(ctx, head.FamilyID, req.Id); err != nil {
		return nil, err
	}
	return RevokeInvite204Response{}, nil
}
