package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/debts"
)

// Debts implements debts.Store in memory.
type Debts struct {
	mu        sync.Mutex
	items     map[int64]debts.Debt
	payments  []debts.Payment
	snapshots map[int64]map[time.Time]debts.Snapshot
	nextID    int64
}

var _ debts.Store = (*Debts)(nil)

func NewDebts() *Debts {
	return &Debts{items: map[int64]debts.Debt{}, snapshots: map[int64]map[time.Time]debts.Snapshot{}}
}

func (r *Debts) withBalance(d debts.Debt) debts.Debt {
	d.Balance = d.Principal
	for _, p := range r.payments {
		if p.DebtID == d.ID {
			d.Balance = d.Balance.Sub(p.Amount)
		}
	}
	return d
}

func (r *Debts) collect(keep func(debts.Debt) bool) []debts.Debt {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []debts.Debt
	for _, d := range r.items {
		if d = r.withBalance(d); keep(d) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b debts.Debt) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (r *Debts) List(_ context.Context, userID int64) ([]debts.Debt, error) {
	return r.collect(func(d debts.Debt) bool { return d.UserID == userID }), nil
}

func (r *Debts) Open(context.Context) ([]debts.Debt, error) {
	return r.collect(func(d debts.Debt) bool { return d.Balance.IsPositive() }), nil
}

func (r *Debts) Get(_ context.Context, userID, id int64) (debts.Debt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.items[id]
	if !ok || d.UserID != userID {
		return debts.Debt{}, debts.ErrNotFound
	}
	return r.withBalance(d), nil
}

func (r *Debts) Create(_ context.Context, d debts.Debt) (debts.Debt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	d.ID = r.nextID
	r.items[d.ID] = d
	return r.withBalance(d), nil
}

func (r *Debts) Update(_ context.Context, d debts.Debt) (debts.Debt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.items[d.ID]; !ok || old.UserID != d.UserID {
		return debts.Debt{}, debts.ErrNotFound
	}
	r.items[d.ID] = d
	return r.withBalance(d), nil
}

func (r *Debts) Delete(_ context.Context, userID, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.items[id]; !ok || d.UserID != userID {
		return debts.ErrNotFound
	}
	delete(r.items, id)
	r.payments = slices.DeleteFunc(r.payments, func(p debts.Payment) bool { return p.DebtID == id })
	return nil
}

func (r *Debts) AddPayment(_ context.Context, p debts.Payment) (debts.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	p.ID = r.nextID
	r.payments = append(r.payments, p)
	return p, nil
}

func (r *Debts) Payments(_ context.Context, userID, debtID int64) ([]debts.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []debts.Payment
	for _, p := range r.payments {
		if p.UserID == userID && p.DebtID == debtID {
			out = append(out, p)
		}
	}
	slices.Reverse(out)
	return out, nil
}

func (r *Debts) DeletePayment(_ context.Context, userID, id int64) (debts.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.payments {
		if p.ID == id && p.UserID == userID {
			r.payments = slices.Delete(r.payments, i, i+1)
			return p, nil
		}
	}
	return debts.Payment{}, debts.ErrNotFound
}

func (r *Debts) ClosedUsers(context.Context) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []int64
	for _, d := range r.items {
		if d.ClosedAt != nil {
			ids = append(ids, d.UserID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (r *Debts) SaveSnapshot(_ context.Context, userID int64, s debts.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snapshots[userID] == nil {
		r.snapshots[userID] = map[time.Time]debts.Snapshot{}
	}
	r.snapshots[userID][s.Month] = s
	return nil
}

func (r *Debts) Snapshots(_ context.Context, userID int64, from time.Time) ([]debts.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []debts.Snapshot
	for m, s := range r.snapshots[userID] {
		if !m.Before(from) {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b debts.Snapshot) int { return a.Month.Compare(b.Month) })
	return out, nil
}
