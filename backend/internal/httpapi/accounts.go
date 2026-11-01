package httpapi

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
)

func toAPIAccount(b accounts.Balance) Account {
	return Account{
		Id: b.ID, Name: b.Name, Kind: AccountKind(b.Kind), Currency: Currency(b.Currency),
		IsDefault: b.IsDefault, Balance: b.Amount, BalanceRub: b.Rubles,
	}
}

func (s *Server) ListAccounts(ctx context.Context, _ ListAccountsRequestObject) (ListAccountsResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	list, total, err := s.accounts.Balances(ctx, u.TelegramID)
	if err != nil {
		return nil, err
	}
	items := make([]Account, len(list))
	for i, b := range list {
		items[i] = toAPIAccount(b)
	}
	return ListAccounts200JSONResponse{Items: items, TotalRub: total}, nil
}

// account returns one account with its balance.
func (s *Server) account(ctx context.Context, userID, id int64) (Account, error) {
	list, _, err := s.accounts.Balances(ctx, userID)
	if err != nil {
		return Account{}, err
	}
	for _, b := range list {
		if b.ID == id {
			return toAPIAccount(b), nil
		}
	}
	// Archived just now: nothing lies on it for the screen any more.
	return Account{}, accounts.ErrNotFound
}

func (s *Server) CreateAccount(ctx context.Context, req CreateAccountRequestObject) (CreateAccountResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	a := accounts.Account{UserID: u.TelegramID, Name: b.Name}
	if b.Kind != nil {
		a.Kind = string(*b.Kind)
	}
	if b.Currency != nil {
		a.Currency = string(*b.Currency)
	}
	if b.Balance != nil {
		a.Opening = *b.Balance
	}
	saved, err := s.accounts.Create(ctx, a)
	if err != nil {
		return nil, err
	}
	out, err := s.account(ctx, u.TelegramID, saved.ID)
	if err != nil {
		return nil, err
	}
	return CreateAccount201JSONResponse(out), nil
}

func (s *Server) UpdateAccount(ctx context.Context, req UpdateAccountRequestObject) (UpdateAccountResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := accounts.Patch{Name: b.Name, Balance: b.Balance, Archived: b.Archived}
	if b.Kind != nil {
		k := string(*b.Kind)
		p.Kind = &k
	}
	saved, err := s.accounts.Update(ctx, u.TelegramID, req.Id, p, s.family.Now())
	if err != nil {
		return nil, err
	}
	if saved.ArchivedAt != nil {
		return UpdateAccount200JSONResponse(toAPIAccount(accounts.Balance{Account: saved})), nil
	}
	out, err := s.account(ctx, u.TelegramID, req.Id)
	if err != nil {
		return nil, err
	}
	return UpdateAccount200JSONResponse(out), nil
}

func toAPITransfer(t accounts.Transfer) Transfer {
	return Transfer{
		Id: t.ID, FromAccountId: t.FromID, ToAccountId: t.ToID, AmountFrom: t.AmountFrom, AmountTo: t.AmountTo,
		Date: openapi_types.Date{Time: t.Date}, Comment: optional(t.Comment),
	}
}

func (s *Server) ListTransfers(ctx context.Context, _ ListTransfersRequestObject) (ListTransfersResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	list, err := s.accounts.Transfers(ctx, u.TelegramID)
	if err != nil {
		return nil, err
	}
	items := make([]Transfer, len(list))
	for i, t := range list {
		items[i] = toAPITransfer(t)
	}
	return ListTransfers200JSONResponse{Items: items}, nil
}

func (s *Server) CreateTransfer(ctx context.Context, req CreateTransferRequestObject) (CreateTransferResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := accounts.TransferInput{UserID: u.TelegramID, FromID: b.FromAccountId, ToID: b.ToAccountId, AmountFrom: b.AmountFrom,
		Comment: deref(b.Comment), Today: s.today(u)}
	if b.AmountTo != nil {
		in.AmountTo = *b.AmountTo
	}
	if b.Date != nil {
		in.Date = b.Date.Time
	}
	t, err := s.accounts.Transfer(ctx, in)
	if err != nil {
		return nil, err
	}
	s.events.Track(ctx, u.TelegramID, events.TransferAdded, nil)
	return CreateTransfer201JSONResponse(toAPITransfer(t)), nil
}

func (s *Server) DeleteTransfer(ctx context.Context, req DeleteTransferRequestObject) (DeleteTransferResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.accounts.DeleteTransfer(ctx, u.TelegramID, req.Id); err != nil {
		return nil, err
	}
	return DeleteTransfer204Response{}, nil
}

func (s *Server) ListRates(ctx context.Context, _ ListRatesRequestObject) (ListRatesResponseObject, error) {
	if _, err := consented(ctx); err != nil {
		return nil, err
	}
	list, err := s.rates.Latest(ctx, rates.Codes)
	if err != nil {
		return nil, err
	}
	items := make([]Rate, len(list))
	for i, r := range list {
		items[i] = Rate{Code: Currency(r.Code), Value: r.Value.Round(2), Date: openapi_types.Date{Time: r.Date}}
	}
	return ListRates200JSONResponse{Items: items}, nil
}
