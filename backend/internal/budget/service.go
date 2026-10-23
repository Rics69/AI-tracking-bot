package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// Repository persists plans, goals and regular payments. Every method works
// within one user's data: someone else's goal is ErrNotFound.
type Repository interface {
	Plan(ctx context.Context, userID int64) (Plan, error) // ErrNotFound: no plan
	SavePlan(ctx context.Context, p Plan) (Plan, error)
	DeletePlan(ctx context.Context, userID int64) error

	// Goals lists the user's goals, oldest first, with Saved filled in.
	Goals(ctx context.Context, userID int64) ([]Goal, error)
	Goal(ctx context.Context, userID, id int64) (Goal, error)
	CreateGoal(ctx context.Context, g Goal) (Goal, error)
	UpdateGoal(ctx context.Context, g Goal) (Goal, error)
	DeleteGoal(ctx context.Context, userID, id int64) error
	// AddDeposit stores a deposit and the goal's new AchievedOn in one transaction.
	AddDeposit(ctx context.Context, d Deposit, achievedOn *time.Time) (Goal, error)
	// Saved is the net amount the user deposited to goals between two dates, inclusive.
	Saved(ctx context.Context, userID int64, from, to time.Time) (decimal.Decimal, error)

	// Payments lists the user's regular payments by day of the month, with LastPaidOn.
	Payments(ctx context.Context, userID int64) ([]RegularPayment, error)
	Payment(ctx context.Context, userID, id int64) (RegularPayment, error)
	CreatePayment(ctx context.Context, p RegularPayment) (RegularPayment, error)
	UpdatePayment(ctx context.Context, p RegularPayment) (RegularPayment, error)
	DeletePayment(ctx context.Context, userID, id int64) error

	// Reminded lists users who may get a daily reminder: with reminders on in
	// their plan, or with regular payments.
	Reminded(ctx context.Context) ([]int64, error)
}

// Ledger is the part of ledger.Service the budget needs.
type Ledger interface {
	Create(ctx context.Context, in ledger.CreateInput) (ledger.Operation, error)
	List(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error)
}

// Service implements goals, plans and regular payments, and computes the limit.
type Service struct {
	repo   Repository
	ledger Ledger
}

func NewService(repo Repository, l Ledger) *Service {
	return &Service{repo: repo, ledger: l}
}

// Plan returns the user's plan, or nil if they have not set one.
func (s *Service) Plan(ctx context.Context, userID int64) (*Plan, error) {
	p, err := s.repo.Plan(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SetPlan validates and stores the user's plan, replacing the old one.
func (s *Service) SetPlan(ctx context.Context, p Plan) (Plan, error) {
	if err := p.normalize(); err != nil {
		return Plan{}, err
	}
	return s.repo.SavePlan(ctx, p)
}

// DeletePlan turns the limit off; goals and regular payments stay.
func (s *Service) DeletePlan(ctx context.Context, userID int64) error {
	err := s.repo.DeletePlan(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Budget computes the user's budget for today, or returns nil without a plan.
func (s *Service) Budget(ctx context.Context, userID int64, today time.Time) (*Budget, error) {
	plan, err := s.Plan(ctx, userID)
	if err != nil || plan == nil {
		return nil, err
	}
	start, _ := Period(today, plan.Paydays)
	ops, err := s.operations(ctx, userID, start, today)
	if err != nil {
		return nil, err
	}
	payments, err := s.repo.Payments(ctx, userID)
	if err != nil {
		return nil, err
	}
	saved, err := s.repo.Saved(ctx, userID, start, today)
	if err != nil {
		return nil, err
	}
	b := Compute(Input{Today: today, Plan: *plan, Payments: payments, Operations: ops, Saved: saved})
	return &b, nil
}

// operations loads all the user's operations between two dates, page by page.
func (s *Service) operations(ctx context.Context, userID int64, from, to time.Time) ([]ledger.Operation, error) {
	var out []ledger.Operation
	for {
		page, err := s.ledger.List(ctx, ledger.OwnedBy(userID), ledger.Filter{
			From: &from, To: &to, Limit: ledger.MaxLimit, Offset: len(out),
		})
		if err != nil {
			return nil, fmt.Errorf("list operations: %w", err)
		}
		out = append(out, page.Items...)
		if len(page.Items) == 0 || len(out) >= page.Total {
			return out, nil
		}
	}
}

// Goals.

func (s *Service) Goals(ctx context.Context, userID int64) ([]Goal, error) {
	return s.repo.Goals(ctx, userID)
}

func (s *Service) Goal(ctx context.Context, userID, id int64) (Goal, error) {
	return s.repo.Goal(ctx, userID, id)
}

// CreateGoal adds a goal; today is the user's date, for the deadline check.
func (s *Service) CreateGoal(ctx context.Context, g Goal, today time.Time) (Goal, error) {
	if err := g.normalize(today); err != nil {
		return Goal{}, err
	}
	goals, err := s.repo.Goals(ctx, g.UserID)
	if err != nil {
		return Goal{}, err
	}
	if len(goals) >= MaxGoals {
		return Goal{}, invalid("goals", "at most %d goals", MaxGoals)
	}
	return s.repo.CreateGoal(ctx, g)
}

// GoalPatch holds the fields to change; nil fields are left as is.
type GoalPatch struct {
	Title    *string
	Target   *decimal.Decimal
	Deadline *time.Time
	// NoDeadline removes the deadline.
	NoDeadline bool
}

func (s *Service) UpdateGoal(ctx context.Context, userID, id int64, p GoalPatch, today time.Time) (Goal, error) {
	g, err := s.repo.Goal(ctx, userID, id)
	if err != nil {
		return Goal{}, err
	}
	if p.Title != nil {
		g.Title = *p.Title
	}
	if p.Target != nil {
		g.Target = *p.Target
	}
	if p.Deadline != nil {
		g.Deadline = p.Deadline
	}
	if p.NoDeadline {
		g.Deadline = nil
	}
	if err := g.normalize(today); err != nil {
		return Goal{}, err
	}
	g.AchievedOn = achieved(g, g.Saved, today)
	return s.repo.UpdateGoal(ctx, g)
}

func (s *Service) DeleteGoal(ctx context.Context, userID, id int64) error {
	return s.repo.DeleteGoal(ctx, userID, id)
}

// Deposit puts money aside for a goal, or takes it back when the amount is
// negative. date nil means today; today is the user's date.
func (s *Service) Deposit(ctx context.Context, userID, goalID int64, amount decimal.Decimal, date *time.Time, today time.Time) (Goal, error) {
	g, err := s.repo.Goal(ctx, userID, goalID)
	if err != nil {
		return Goal{}, err
	}
	if amount.IsZero() {
		return Goal{}, invalid("amount", "must not be zero")
	}
	if err := checkAmount("amount", amount.Abs()); err != nil {
		return Goal{}, err
	}
	d := today
	if date != nil {
		d = dateOf(*date)
	}
	if d.After(today) {
		return Goal{}, invalid("date", "must not be in the future")
	}
	saved := g.Saved.Add(amount)
	if saved.IsNegative() {
		return Goal{}, invalid("amount", "only %s is saved for this goal", g.Saved.StringFixed(2))
	}
	return s.repo.AddDeposit(ctx, Deposit{GoalID: g.ID, UserID: userID, Amount: amount, Date: d}, achieved(g, saved, today))
}

// achieved keeps the date a goal was first reached, sets it when the goal is
// reached now, and clears it when the savings fall below the target.
func achieved(g Goal, saved decimal.Decimal, today time.Time) *time.Time {
	switch {
	case saved.LessThan(g.Target):
		return nil
	case g.AchievedOn != nil:
		return g.AchievedOn
	default:
		return &today
	}
}

// Regular payments.

func (s *Service) Payments(ctx context.Context, userID int64) ([]RegularPayment, error) {
	return s.repo.Payments(ctx, userID)
}

func (s *Service) Payment(ctx context.Context, userID, id int64) (RegularPayment, error) {
	return s.repo.Payment(ctx, userID, id)
}

func (s *Service) CreatePayment(ctx context.Context, p RegularPayment) (RegularPayment, error) {
	if err := p.normalize(); err != nil {
		return RegularPayment{}, err
	}
	payments, err := s.repo.Payments(ctx, p.UserID)
	if err != nil {
		return RegularPayment{}, err
	}
	if len(payments) >= MaxPayments {
		return RegularPayment{}, invalid("regular_payments", "at most %d regular payments", MaxPayments)
	}
	return s.repo.CreatePayment(ctx, p)
}

// PaymentPatch holds the fields to change; nil fields are left as is.
type PaymentPatch struct {
	Title    *string
	Amount   *decimal.Decimal
	Day      *int
	Category *ledger.Category
}

func (s *Service) UpdatePayment(ctx context.Context, userID, id int64, p PaymentPatch) (RegularPayment, error) {
	rp, err := s.repo.Payment(ctx, userID, id)
	if err != nil {
		return RegularPayment{}, err
	}
	if p.Title != nil {
		rp.Title = *p.Title
	}
	if p.Amount != nil {
		rp.Amount = *p.Amount
	}
	if p.Day != nil {
		rp.Day = *p.Day
	}
	if p.Category != nil {
		rp.Category = *p.Category
	}
	if err := rp.normalize(); err != nil {
		return RegularPayment{}, err
	}
	return s.repo.UpdatePayment(ctx, rp)
}

// DeletePayment removes a regular payment; expenses that paid it stay, unlinked.
func (s *Service) DeletePayment(ctx context.Context, userID, id int64) error {
	return s.repo.DeletePayment(ctx, userID, id)
}

// Pay records today's expense for a regular payment: its amount, category and title.
func (s *Service) Pay(ctx context.Context, userID, id int64, today time.Time) (ledger.Operation, error) {
	p, err := s.repo.Payment(ctx, userID, id)
	if err != nil {
		return ledger.Operation{}, err
	}
	return s.ledger.Create(ctx, ledger.CreateInput{
		UserID: userID, Amount: p.Amount, Category: p.Category, Comment: p.Title,
		RegularPaymentID: &p.ID, Date: &today, Today: today,
	})
}

// Reminded lists users who may get a daily reminder.
func (s *Service) Reminded(ctx context.Context) ([]int64, error) {
	return s.repo.Reminded(ctx)
}
