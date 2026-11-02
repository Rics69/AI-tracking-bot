package httpapi

import (
	"context"
	"errors"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

func toAPIDebt(d debts.Debt) Debt {
	return Debt{Id: d.ID, Name: d.Name, Kind: DebtKind(d.Kind), Balance: d.Balance, Rate: d.Rate, MinPayment: d.MinPayment, DueDay: d.DueDay}
}

func (s *Server) ListDebts(ctx context.Context, _ ListDebtsRequestObject) (ListDebtsResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	list, total, err := s.debts.List(ctx, u.TelegramID)
	if err != nil {
		return nil, err
	}
	items := make([]Debt, len(list))
	for i, d := range list {
		items[i] = toAPIDebt(d)
	}
	return ListDebts200JSONResponse{Items: items, Total: total}, nil
}

func (s *Server) CreateDebt(ctx context.Context, req CreateDebtRequestObject) (CreateDebtResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	d := debts.Debt{UserID: u.TelegramID, Name: b.Name, Principal: b.Balance, Rate: orElse(b.Rate, decimal.Zero),
		MinPayment: orElse(b.MinPayment, decimal.Zero), DueDay: b.DueDay}
	if b.Kind != nil {
		d.Kind = string(*b.Kind)
	}
	saved, err := s.debts.Create(ctx, d, s.today(u))
	if err != nil {
		return nil, err
	}
	return CreateDebt201JSONResponse(toAPIDebt(saved)), nil
}

func (s *Server) UpdateDebt(ctx context.Context, req UpdateDebtRequestObject) (UpdateDebtResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := debts.Patch{Name: b.Name, Rate: b.Rate, MinPayment: b.MinPayment, DueDay: b.DueDay, Balance: b.Balance}
	if b.Kind != nil {
		k := string(*b.Kind)
		p.Kind = &k
	}
	saved, err := s.debts.Update(ctx, u.TelegramID, req.Id, p, s.family.Now())
	if err != nil {
		return nil, err
	}
	return UpdateDebt200JSONResponse(toAPIDebt(saved)), nil
}

func (s *Server) DeleteDebt(ctx context.Context, req DeleteDebtRequestObject) (DeleteDebtResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.debts.Delete(ctx, u.TelegramID, req.Id); err != nil {
		return nil, err
	}
	return DeleteDebt204Response{}, nil
}

func (s *Server) PayDebt(ctx context.Context, req PayDebtRequestObject) (PayDebtResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	paid, err := s.debts.Pay(ctx, debts.PaymentInput{UserID: u.TelegramID, DebtID: req.Id, Amount: b.Amount, Date: dateTime(b.Date),
		AccountID: b.AccountId, Today: s.today(u), Now: s.family.Now()})
	if err != nil {
		return nil, err
	}
	return PayDebt201JSONResponse(toAPIDebt(paid)), nil
}

func (s *Server) ListDebtPayments(ctx context.Context, req ListDebtPaymentsRequestObject) (ListDebtPaymentsResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	list, err := s.debts.Payments(ctx, u.TelegramID, req.Id)
	if err != nil {
		return nil, err
	}
	items := make([]DebtPaymentRecord, len(list))
	for i, p := range list {
		items[i] = DebtPaymentRecord{Id: p.ID, Amount: p.Amount, Date: openapi_types.Date{Time: p.Date}}
	}
	return ListDebtPayments200JSONResponse{Items: items}, nil
}

func (s *Server) CancelDebtPayment(ctx context.Context, req CancelDebtPaymentRequestObject) (CancelDebtPaymentResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.debts.CancelPayment(ctx, u.TelegramID, req.Id); err != nil {
		return nil, err
	}
	return CancelDebtPayment204Response{}, nil
}

func toAPISchedule(p debts.Schedule) DebtSchedule {
	closings := make([]DebtClosing, len(p.Closings))
	for i, c := range p.Closings {
		closings[i] = DebtClosing{Name: c.Name, Month: openapi_types.Date{Time: c.Month}}
	}
	return DebtSchedule{Months: p.Months, Interest: p.Interest, Closings: closings}
}

func (s *Server) GetDebtPlan(ctx context.Context, req GetDebtPlanRequestObject) (GetDebtPlanResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	extra := decimal.Zero
	if req.Params.Extra != nil {
		if extra, err = decimal.NewFromString(*req.Params.Extra); err != nil {
			return nil, &ledger.ValidationError{Field: "extra", Message: "must be a number"}
		}
	}
	avalanche, snowball, err := s.debts.Plan(ctx, u.TelegramID, extra, s.today(u))
	if errors.Is(err, debts.ErrNeverPaid) {
		return nil, &ledger.ValidationError{Field: "extra", Message: "платежи не покрывают проценты: долг не уменьшается"}
	}
	if err != nil {
		return nil, err
	}
	return GetDebtPlan200JSONResponse{Avalanche: toAPISchedule(avalanche), Snowball: toAPISchedule(snowball)}, nil
}

func (s *Server) GetNetWorth(ctx context.Context, _ GetNetWorthRequestObject) (GetNetWorthResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	now, err := s.debts.NetWorth(ctx, u.TelegramID, s.today(u))
	if err != nil {
		return nil, err
	}
	past, err := s.debts.History(ctx, u.TelegramID, s.today(u))
	if err != nil {
		return nil, err
	}
	history := make([]NetWorthPoint, len(past))
	for i, p := range past {
		history[i] = NetWorthPoint{Month: openapi_types.Date{Time: p.Month}, Net: p.Net()}
	}
	return GetNetWorth200JSONResponse{Assets: now.Assets, Debts: now.Debts, Net: now.Net(), History: history}, nil
}
