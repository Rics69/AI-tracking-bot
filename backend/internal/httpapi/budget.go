package httpapi

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// BudgetService manages goals, the plan and regular payments; *budget.Service implements it.
// Everything there is personal: even a family head sees only their own.
type BudgetService interface {
	Plan(ctx context.Context, userID int64) (*budget.Plan, error)
	SetPlan(ctx context.Context, p budget.Plan) (budget.Plan, error)
	DeletePlan(ctx context.Context, userID int64) error
	Budget(ctx context.Context, userID int64, today time.Time) (*budget.Budget, error)

	Goals(ctx context.Context, userID int64) ([]budget.Goal, error)
	CreateGoal(ctx context.Context, g budget.Goal, today time.Time) (budget.Goal, error)
	UpdateGoal(ctx context.Context, userID, id int64, p budget.GoalPatch, today time.Time) (budget.Goal, error)
	DeleteGoal(ctx context.Context, userID, id int64) error
	Deposit(ctx context.Context, userID, goalID int64, amount decimal.Decimal, date *time.Time, today time.Time) (budget.Goal, error)

	Payments(ctx context.Context, userID int64) ([]budget.RegularPayment, error)
	CreatePayment(ctx context.Context, p budget.RegularPayment) (budget.RegularPayment, error)
	UpdatePayment(ctx context.Context, userID, id int64, p budget.PaymentPatch) (budget.RegularPayment, error)
	DeletePayment(ctx context.Context, userID, id int64) error
	Pay(ctx context.Context, userID, id int64, today time.Time) (ledger.Operation, error)
}

func (s *Server) GetBudget(ctx context.Context, _ GetBudgetRequestObject) (GetBudgetResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := s.budget.Plan(ctx, u.TelegramID)
	if err != nil || plan == nil {
		return GetBudget200JSONResponse{}, err
	}
	b, err := s.budget.Budget(ctx, u.TelegramID, s.today(u))
	if err != nil {
		return nil, err
	}
	apiPlan := toAPIPlan(*plan)
	today := toAPIBudget(*b)
	return GetBudget200JSONResponse{Plan: &apiPlan, Today: &today}, nil
}

func (s *Server) SetPlan(ctx context.Context, req SetPlanRequestObject) (SetPlanResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := budget.Plan{
		UserID:           u.TelegramID,
		Paydays:          b.Paydays,
		ExpectedIncome:   b.ExpectedIncome,
		GoalContribution: b.GoalContribution,
		ReminderHour:     budget.DefaultReminderHour,
		Reminders:        true,
	}
	if b.ReminderHour != nil {
		p.ReminderHour = *b.ReminderHour
	}
	if b.Reminders != nil {
		p.Reminders = *b.Reminders
	}
	saved, err := s.budget.SetPlan(ctx, p)
	if err != nil {
		return nil, err
	}
	return SetPlan200JSONResponse(toAPIPlan(saved)), nil
}

func (s *Server) DeletePlan(ctx context.Context, _ DeletePlanRequestObject) (DeletePlanResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.budget.DeletePlan(ctx, u.TelegramID); err != nil {
		return nil, err
	}
	return DeletePlan204Response{}, nil
}

// Goals.

func (s *Server) ListGoals(ctx context.Context, _ ListGoalsRequestObject) (ListGoalsResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	goals, err := s.budget.Goals(ctx, u.TelegramID)
	if err != nil {
		return nil, err
	}
	items := make([]Goal, len(goals))
	for i, g := range goals {
		items[i] = toAPIGoal(g)
	}
	return ListGoals200JSONResponse{Items: items}, nil
}

func (s *Server) CreateGoal(ctx context.Context, req CreateGoalRequestObject) (CreateGoalResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	g, err := s.budget.CreateGoal(ctx, budget.Goal{
		UserID: u.TelegramID, Title: b.Title, Target: b.Target, Deadline: dateTime(b.Deadline),
	}, s.today(u))
	if err != nil {
		return nil, err
	}
	return CreateGoal201JSONResponse(toAPIGoal(g)), nil
}

func (s *Server) UpdateGoal(ctx context.Context, req UpdateGoalRequestObject) (UpdateGoalResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	g, err := s.budget.UpdateGoal(ctx, u.TelegramID, req.Id, budget.GoalPatch{
		Title: b.Title, Target: b.Target, Deadline: dateTime(b.Deadline), NoDeadline: deref(b.ClearDeadline),
	}, s.today(u))
	if err != nil {
		return nil, err
	}
	return UpdateGoal200JSONResponse(toAPIGoal(g)), nil
}

func (s *Server) DeleteGoal(ctx context.Context, req DeleteGoalRequestObject) (DeleteGoalResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.budget.DeleteGoal(ctx, u.TelegramID, req.Id); err != nil {
		return nil, err
	}
	return DeleteGoal204Response{}, nil
}

func (s *Server) DepositToGoal(ctx context.Context, req DepositToGoalRequestObject) (DepositToGoalResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	g, err := s.budget.Deposit(ctx, u.TelegramID, req.Id, req.Body.Amount, dateTime(req.Body.Date), s.today(u))
	if err != nil {
		return nil, err
	}
	return DepositToGoal200JSONResponse(toAPIGoal(g)), nil
}

// Regular payments.

func (s *Server) ListRegularPayments(ctx context.Context, _ ListRegularPaymentsRequestObject) (ListRegularPaymentsResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	payments, err := s.budget.Payments(ctx, u.TelegramID)
	if err != nil {
		return nil, err
	}
	today := s.today(u)
	items := make([]RegularPayment, len(payments))
	for i, p := range payments {
		items[i] = toAPIPayment(p, today)
	}
	return ListRegularPayments200JSONResponse{Items: items}, nil
}

func (s *Server) CreateRegularPayment(ctx context.Context, req CreateRegularPaymentRequestObject) (CreateRegularPaymentResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := budget.RegularPayment{UserID: u.TelegramID, Title: b.Title, Amount: b.Amount, Day: b.Day}
	if b.Category != nil {
		p.Category = ledger.Category(*b.Category)
	}
	created, err := s.budget.CreatePayment(ctx, p)
	if err != nil {
		return nil, err
	}
	return CreateRegularPayment201JSONResponse(toAPIPayment(created, s.today(u))), nil
}

func (s *Server) UpdateRegularPayment(ctx context.Context, req UpdateRegularPaymentRequestObject) (UpdateRegularPaymentResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := budget.PaymentPatch{Title: b.Title, Amount: b.Amount, Day: b.Day}
	if b.Category != nil {
		c := ledger.Category(*b.Category)
		p.Category = &c
	}
	updated, err := s.budget.UpdatePayment(ctx, u.TelegramID, req.Id, p)
	if err != nil {
		return nil, err
	}
	return UpdateRegularPayment200JSONResponse(toAPIPayment(updated, s.today(u))), nil
}

func (s *Server) DeleteRegularPayment(ctx context.Context, req DeleteRegularPaymentRequestObject) (DeleteRegularPaymentResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.budget.DeletePayment(ctx, u.TelegramID, req.Id); err != nil {
		return nil, err
	}
	return DeleteRegularPayment204Response{}, nil
}

func (s *Server) PayRegularPayment(ctx context.Context, req PayRegularPaymentRequestObject) (PayRegularPaymentResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.budget.Pay(ctx, u.TelegramID, req.Id, s.today(u))
	if err != nil {
		return nil, err
	}
	return PayRegularPayment201JSONResponse(toAPIOperation(o)), nil
}

func toAPIPlan(p budget.Plan) Plan {
	return Plan{
		Paydays:          p.Paydays,
		ExpectedIncome:   p.ExpectedIncome,
		GoalContribution: p.GoalContribution,
		ReminderHour:     p.ReminderHour,
		Reminders:        p.Reminders,
		UpdatedAt:        p.UpdatedAt,
	}
}

func toAPIBudget(b budget.Budget) BudgetToday {
	upcoming := make([]UpcomingPayment, len(b.Upcoming))
	for i, d := range b.Upcoming {
		upcoming[i] = UpcomingPayment{Id: d.Payment.ID, Title: d.Payment.Title, Amount: d.Payment.Amount, Date: date(d.Date)}
	}
	return BudgetToday{
		Ready:          b.Ready,
		PeriodStart:    date(b.PeriodStart),
		NextPayday:     date(b.NextPayday),
		DaysLeft:       b.DaysLeft,
		Income:         b.Income,
		IncomeExpected: b.IncomeExpected,
		Payments:       b.Payments,
		Savings:        b.Savings,
		Spent:          b.Spent,
		SpentToday:     b.SpentToday,
		Available:      b.Available,
		Remaining:      b.Remaining(),
		Limit:          b.Limit,
		Left:           b.Left(),
		Upcoming:       upcoming,
	}
}

func toAPIGoal(g budget.Goal) Goal {
	return Goal{
		Id:         g.ID,
		Title:      g.Title,
		Target:     g.Target,
		Saved:      g.Saved,
		Left:       g.Left(),
		Deadline:   optionalDate(g.Deadline),
		AchievedOn: optionalDate(g.AchievedOn),
		CreatedAt:  g.CreatedAt,
	}
}

func toAPIPayment(p budget.RegularPayment, today time.Time) RegularPayment {
	return RegularPayment{
		Id:         p.ID,
		Title:      p.Title,
		Amount:     p.Amount,
		Day:        p.Day,
		Category:   Category(p.Category),
		DueDate:    date(p.Due(today)),
		LastPaidOn: optionalDate(p.LastPaidOn),
		CreatedAt:  p.CreatedAt,
	}
}

func date(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }

func optionalDate(t *time.Time) *openapi_types.Date {
	if t == nil {
		return nil
	}
	d := date(*t)
	return &d
}
