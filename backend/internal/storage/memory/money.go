package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
)

// Accounts implements accounts.Store in memory.
type Accounts struct {
	Operations *OperationRepository // where Operated looks

	mu        sync.Mutex
	items     map[int64]accounts.Account
	transfers []accounts.Transfer
	nextID    int64
}

var _ accounts.Store = (*Accounts)(nil)

func NewAccounts(ops *OperationRepository) *Accounts {
	return &Accounts{Operations: ops, items: map[int64]accounts.Account{}}
}

func (r *Accounts) EnsureDefault(_ context.Context, userID int64, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.items {
		if a.UserID == userID && a.IsDefault {
			return nil
		}
	}
	r.nextID++
	r.items[r.nextID] = accounts.Account{ID: r.nextID, UserID: userID, Name: name, Kind: accounts.KindCard,
		Currency: ledger.RUB, IsDefault: true, Opening: decimal.Zero, CreatedAt: time.Now()}
	return nil
}

func (r *Accounts) List(_ context.Context, userID int64) ([]accounts.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []accounts.Account
	for _, a := range r.items {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b accounts.Account) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (r *Accounts) Get(_ context.Context, userID, id int64) (accounts.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.items[id]
	if !ok || a.UserID != userID {
		return accounts.Account{}, accounts.ErrNotFound
	}
	return a, nil
}

func (r *Accounts) Create(_ context.Context, a accounts.Account) (accounts.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	a.ID, a.CreatedAt = r.nextID, time.Now()
	r.items[a.ID] = a
	return a, nil
}

func (r *Accounts) Update(_ context.Context, a accounts.Account) (accounts.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.items[a.ID]; !ok || old.UserID != a.UserID {
		return accounts.Account{}, accounts.ErrNotFound
	}
	r.items[a.ID] = a
	return a, nil
}

func (r *Accounts) Operated(_ context.Context, userID int64) (map[int64]decimal.Decimal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[int64]decimal.Decimal{}
	if r.Operations == nil {
		return out, nil
	}
	for _, o := range r.Operations.All() {
		if o.UserID != userID {
			continue
		}
		for _, a := range r.items {
			mine := o.AccountID != nil && *o.AccountID == a.ID || o.AccountID == nil && a.IsDefault
			if a.UserID != userID || !mine {
				continue
			}
			sum := o.Amount
			if a.Currency != ledger.RUB && o.Original != nil {
				sum = *o.Original
			}
			if o.Kind() == ledger.KindExpense {
				sum = sum.Neg()
			}
			out[a.ID] = out[a.ID].Add(sum)
		}
	}
	return out, nil
}

func (r *Accounts) AddTransfer(_ context.Context, t accounts.Transfer) (accounts.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	t.ID = r.nextID
	r.transfers = append(r.transfers, t)
	return t, nil
}

func (r *Accounts) Transfers(_ context.Context, userID int64) ([]accounts.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []accounts.Transfer
	for _, t := range r.transfers {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	slices.Reverse(out)
	return out, nil
}

func (r *Accounts) DeleteTransfer(_ context.Context, userID, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, t := range r.transfers {
		if t.ID == id && t.UserID == userID {
			r.transfers = slices.Delete(r.transfers, i, i+1)
			return nil
		}
	}
	return accounts.ErrNotFound
}

// AllTransfers returns everything stored, for checks in evals.
func (r *Accounts) AllTransfers() []accounts.Transfer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.transfers)
}

// Tasks implements tasks.Store in memory.
type Tasks struct {
	mu     sync.Mutex
	items  map[int64]tasks.Task
	nextID int64
}

var _ tasks.Store = (*Tasks)(nil)

func NewTasks() *Tasks { return &Tasks{items: map[int64]tasks.Task{}} }

func (r *Tasks) collect(keep func(tasks.Task) bool) []tasks.Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []tasks.Task
	for _, t := range r.items {
		if keep(t) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b tasks.Task) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (r *Tasks) List(_ context.Context, userID int64) ([]tasks.Task, error) {
	return r.collect(func(t tasks.Task) bool { return t.UserID == userID }), nil
}

func (r *Tasks) Open(context.Context) ([]tasks.Task, error) {
	return r.collect(func(t tasks.Task) bool { return t.DoneAt == nil }), nil
}

func (r *Tasks) Get(_ context.Context, userID, id int64) (tasks.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.items[id]
	if !ok || t.UserID != userID {
		return tasks.Task{}, tasks.ErrNotFound
	}
	return t, nil
}

func (r *Tasks) Create(_ context.Context, t tasks.Task) (tasks.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	t.ID, t.CreatedAt = r.nextID, time.Now()
	r.items[t.ID] = t
	return t, nil
}

func (r *Tasks) Update(_ context.Context, t tasks.Task) (tasks.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.items[t.ID]; !ok || old.UserID != t.UserID {
		return tasks.Task{}, tasks.ErrNotFound
	}
	r.items[t.ID] = t
	return t, nil
}

func (r *Tasks) Delete(_ context.Context, userID, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.items[id]; !ok || t.UserID != userID {
		return tasks.ErrNotFound
	}
	delete(r.items, id)
	return nil
}
