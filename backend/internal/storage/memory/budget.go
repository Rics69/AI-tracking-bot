package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
)

// Budget implements budget.Repository in memory.
type Budget struct {
	mu       sync.Mutex
	plans    map[int64]budget.Plan
	goals    map[int64]budget.Goal
	deposits []budget.Deposit
	payments map[int64]budget.RegularPayment
	nextID   int64
	// Operations, if set, tells when regular payments were last paid and
	// loses its links when a payment is deleted, like the foreign key.
	Operations *OperationRepository
	// Now stamps created and updated times; nil means time.Now.
	Now func() time.Time
}

var _ budget.Repository = (*Budget)(nil)

func NewBudget(ops *OperationRepository) *Budget {
	return &Budget{
		plans: map[int64]budget.Plan{}, goals: map[int64]budget.Goal{}, payments: map[int64]budget.RegularPayment{},
		Operations: ops,
	}
}

func (r *Budget) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Budget) Plan(_ context.Context, userID int64) (budget.Plan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plans[userID]
	if !ok {
		return budget.Plan{}, budget.ErrNotFound
	}
	return p, nil
}

func (r *Budget) SavePlan(_ context.Context, p budget.Plan) (budget.Plan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.UpdatedAt = r.now()
	r.plans[p.UserID] = p
	return p, nil
}

func (r *Budget) DeletePlan(_ context.Context, userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plans[userID]; !ok {
		return budget.ErrNotFound
	}
	delete(r.plans, userID)
	return nil
}

func (r *Budget) Goals(_ context.Context, userID int64) ([]budget.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []budget.Goal{}
	for _, g := range r.goals {
		if g.UserID == userID {
			out = append(out, r.withSaved(g))
		}
	}
	slices.SortFunc(out, func(a, b budget.Goal) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Budget) Goal(_ context.Context, userID, id int64) (budget.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.goal(userID, id)
}

func (r *Budget) goal(userID, id int64) (budget.Goal, error) {
	g, ok := r.goals[id]
	if !ok || g.UserID != userID {
		return budget.Goal{}, budget.ErrNotFound
	}
	return r.withSaved(g), nil
}

func (r *Budget) withSaved(g budget.Goal) budget.Goal {
	g.Saved = decimal.Zero
	for _, d := range r.deposits {
		if d.GoalID == g.ID {
			g.Saved = g.Saved.Add(d.Amount)
		}
	}
	return g
}

func (r *Budget) CreateGoal(_ context.Context, g budget.Goal) (budget.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	g.ID, g.CreatedAt, g.UpdatedAt = r.nextID, r.now(), r.now()
	r.goals[g.ID] = g
	return r.withSaved(g), nil
}

func (r *Budget) UpdateGoal(_ context.Context, g budget.Goal) (budget.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, err := r.goal(g.UserID, g.ID)
	if err != nil {
		return budget.Goal{}, err
	}
	g.CreatedAt, g.UpdatedAt = old.CreatedAt, r.now()
	r.goals[g.ID] = g
	return r.withSaved(g), nil
}

func (r *Budget) DeleteGoal(_ context.Context, userID, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.goal(userID, id); err != nil {
		return err
	}
	delete(r.goals, id)
	r.deposits = slices.DeleteFunc(r.deposits, func(d budget.Deposit) bool { return d.GoalID == id })
	return nil
}

func (r *Budget) AddDeposit(_ context.Context, d budget.Deposit, achievedOn *time.Time) (budget.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, err := r.goal(d.UserID, d.GoalID)
	if err != nil {
		return budget.Goal{}, err
	}
	r.nextID++
	d.ID, d.CreatedAt = r.nextID, r.now()
	r.deposits = append(r.deposits, d)
	g.AchievedOn = achievedOn
	r.goals[g.ID] = g
	return r.withSaved(g), nil
}

func (r *Budget) Saved(_ context.Context, userID int64, from, to time.Time) (decimal.Decimal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sum := decimal.Zero
	for _, d := range r.deposits {
		if d.UserID == userID && !d.Date.Before(from) && !d.Date.After(to) {
			sum = sum.Add(d.Amount)
		}
	}
	return sum, nil
}

// Deposits returns every deposit in the order they were made, for checks in evals.
func (r *Budget) Deposits() []budget.Deposit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.deposits)
}

func (r *Budget) Payments(_ context.Context, userID int64) ([]budget.RegularPayment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []budget.RegularPayment{}
	for _, p := range r.payments {
		if p.UserID == userID {
			out = append(out, r.withLastPaid(p))
		}
	}
	slices.SortFunc(out, func(a, b budget.RegularPayment) int {
		return cmp.Or(cmp.Compare(a.Day, b.Day), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func (r *Budget) Payment(_ context.Context, userID, id int64) (budget.RegularPayment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.payment(userID, id)
}

func (r *Budget) payment(userID, id int64) (budget.RegularPayment, error) {
	p, ok := r.payments[id]
	if !ok || p.UserID != userID {
		return budget.RegularPayment{}, budget.ErrNotFound
	}
	return r.withLastPaid(p), nil
}

func (r *Budget) withLastPaid(p budget.RegularPayment) budget.RegularPayment {
	p.LastPaidOn = nil
	if r.Operations == nil {
		return p
	}
	for _, o := range r.Operations.All() {
		if o.RegularPaymentID != nil && *o.RegularPaymentID == p.ID && (p.LastPaidOn == nil || o.Date.After(*p.LastPaidOn)) {
			d := o.Date
			p.LastPaidOn = &d
		}
	}
	return p
}

func (r *Budget) CreatePayment(_ context.Context, p budget.RegularPayment) (budget.RegularPayment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	p.ID, p.CreatedAt = r.nextID, r.now()
	r.payments[p.ID] = p
	return r.withLastPaid(p), nil
}

func (r *Budget) UpdatePayment(_ context.Context, p budget.RegularPayment) (budget.RegularPayment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, err := r.payment(p.UserID, p.ID)
	if err != nil {
		return budget.RegularPayment{}, err
	}
	p.CreatedAt = old.CreatedAt
	r.payments[p.ID] = p
	return r.withLastPaid(p), nil
}

func (r *Budget) DeletePayment(_ context.Context, userID, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.payment(userID, id); err != nil {
		return err
	}
	delete(r.payments, id)
	if r.Operations != nil {
		r.Operations.unlinkPayment(id)
	}
	return nil
}

func (r *Budget) Reminded(context.Context) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []int64
	for _, p := range r.plans {
		if p.Reminders {
			ids = append(ids, p.UserID)
		}
	}
	for _, p := range r.payments {
		ids = append(ids, p.UserID)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// AllPayments and AllGoals return everything stored, for checks in evals.
func (r *Budget) AllPayments() []budget.RegularPayment {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]budget.RegularPayment, 0, len(r.payments))
	for _, p := range r.payments {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b budget.RegularPayment) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (r *Budget) AllGoals() []budget.Goal {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]budget.Goal, 0, len(r.goals))
	for _, g := range r.goals {
		out = append(out, r.withSaved(g))
	}
	slices.SortFunc(out, func(a, b budget.Goal) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// unlinkPayment forgets a deleted regular payment, like ON DELETE SET NULL.
func (r *OperationRepository) unlinkPayment(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, o := range r.items {
		if o.RegularPaymentID != nil && *o.RegularPaymentID == id {
			o.RegularPaymentID = nil
			r.items[k] = o
		}
	}
}
