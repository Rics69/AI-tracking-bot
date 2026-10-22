package httpapi

import (
	"context"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func (s *Server) GetFamily(ctx context.Context, _ GetFamilyRequestObject) (GetFamilyResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	members, err := s.family.Members(ctx, u.FamilyID)
	if err != nil {
		return nil, err
	}
	out := Family{Id: u.FamilyID, Members: make([]Member, len(members)), Invites: []Invite{}}
	for i, m := range members {
		out.Members[i] = toAPIMember(m)
	}
	if u.IsHead {
		invites, err := s.family.ListInvites(ctx, u.FamilyID)
		if err != nil {
			return nil, err
		}
		now := s.family.Now()
		for _, inv := range invites {
			out.Invites = append(out.Invites, Invite{
				Id:        inv.ID,
				Status:    InviteStatus(inv.Status(now)),
				CreatedAt: inv.CreatedAt,
				ExpiresAt: inv.ExpiresAt,
				UsedAt:    inv.UsedAt,
				UsedBy:    inv.UsedBy,
			})
		}
	}
	return GetFamily200JSONResponse(out), nil
}

func (s *Server) CreateInvite(ctx context.Context, _ CreateInviteRequestObject) (CreateInviteResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	inv, err := s.family.CreateInvite(ctx, u)
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
	u, err := head(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.family.RevokeInvite(ctx, u.FamilyID, req.Id); err != nil {
		return nil, err
	}
	return RevokeInvite204Response{}, nil
}

func (s *Server) UpdateMember(ctx context.Context, req UpdateMemberRequestObject) (UpdateMemberResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.family.SetWeeklyReport(ctx, u, req.TelegramId, req.Body.WeeklyReport)
	if err != nil {
		return nil, err
	}
	return UpdateMember200JSONResponse(toAPIMember(m)), nil
}

func (s *Server) RemoveMember(ctx context.Context, req RemoveMemberRequestObject) (RemoveMemberResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.family.Remove(ctx, u, req.TelegramId); err != nil {
		return nil, err
	}
	return RemoveMember204Response{}, nil
}

func (s *Server) LeaveFamily(ctx context.Context, _ LeaveFamilyRequestObject) (LeaveFamilyResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.family.Leave(ctx, u.TelegramID); err != nil {
		return nil, err
	}
	return LeaveFamily204Response{}, nil
}

func (s *Server) TransferHead(ctx context.Context, req TransferHeadRequestObject) (TransferHeadResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.family.TransferHead(ctx, u, req.Body.TelegramId); err != nil {
		return nil, err
	}
	return TransferHead204Response{}, nil
}

// head returns the caller, who must head their family.
func head(ctx context.Context) (user.User, error) {
	u, err := consented(ctx)
	if err != nil {
		return user.User{}, err
	}
	if !u.IsHead {
		return user.User{}, family.ErrNotHead
	}
	return u, nil
}

func toAPIMember(u user.User) Member {
	return Member{
		TelegramId:   u.TelegramID,
		FirstName:    u.FirstName,
		Username:     optional(u.Username),
		IsHead:       u.IsHead,
		WeeklyReport: u.WeeklyReport,
		CreatedAt:    u.CreatedAt,
	}
}
