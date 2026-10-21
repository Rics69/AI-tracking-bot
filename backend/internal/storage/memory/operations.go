// Package memory is an in-memory ledger.Repository. It mirrors the semantics
// of the PostgreSQL repository and is used where a database is overkill:
// agent evals run every case on a fresh, isolated store.
package memory

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
)

// OperationRepository implements ledger.Repository in memory.
type OperationRepository struct {
	mu     sync.Mutex
	items  map[int64]ledger.Operation
	nextID int64
	// families maps users to their family, standing in for the users table.
	families map[int64]int64
	clock    time.Time // creation timestamps: strictly increasing, so "last added" is stable
}

var _ ledger.Repository = (*OperationRepository)(nil)

func NewOperationRepository() *OperationRepository {
	return &OperationRepository{items: map[int64]ledger.Operation{}, families: map[int64]int64{}, clock: time.Now()}
}

func (r *OperationRepository) Create(_ context.Context, e ledger.Operation) (ledger.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.clock = r.clock.Add(time.Millisecond)
	e.ID = r.nextID
	e.CreatedAt, e.UpdatedAt = r.clock, r.clock
	r.items[e.ID] = e
	return e, nil
}

func (r *OperationRepository) Get(_ context.Context, s ledger.Scope, id int64) (ledger.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[id]
	if !ok || !r.visible(s, e) {
		return ledger.Operation{}, ledger.ErrNotFound
	}
	return e, nil
}

func (r *OperationRepository) Update(_ context.Context, s ledger.Scope, e ledger.Operation) (ledger.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.items[e.ID]
	if !ok || !r.visible(s, old) || !r.visible(s, e) {
		return ledger.Operation{}, ledger.ErrNotFound
	}
	r.clock = r.clock.Add(time.Millisecond)
	e.CreatedAt, e.UpdatedAt = old.CreatedAt, r.clock
	r.items[e.ID] = e
	return e, nil
}

func (r *OperationRepository) Delete(_ context.Context, s ledger.Scope, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[id]
	if !ok || !r.visible(s, e) {
		return ledger.ErrNotFound
	}
	delete(r.items, id)
	return nil
}

func (r *OperationRepository) List(_ context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error) {
	matched := r.match(s, f)
	switch f.Sort {
	case ledger.SortByCreatedAt:
		slices.SortFunc(matched, func(a, b ledger.Operation) int {
			return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
		})
	case ledger.SortByAmount:
		slices.SortFunc(matched, func(a, b ledger.Operation) int {
			return cmp.Or(b.Amount.Cmp(a.Amount), cmp.Compare(b.ID, a.ID))
		})
	default:
		slices.SortFunc(matched, func(a, b ledger.Operation) int {
			return cmp.Or(b.Date.Compare(a.Date), cmp.Compare(b.ID, a.ID))
		})
	}
	page := ledger.Page{Items: []ledger.Operation{}, Total: len(matched), Sum: decimal.Zero}
	for _, e := range matched {
		page.Sum = page.Sum.Add(e.Amount)
	}
	if f.Offset < len(matched) {
		page.Items = matched[f.Offset:min(f.Offset+f.Limit, len(matched))]
	}
	return page, nil
}

func (r *OperationRepository) Summary(_ context.Context, s ledger.Scope, f ledger.Filter) (ledger.Summary, error) {
	byCategory := map[ledger.Category]*ledger.CategoryTotal{}
	sum := ledger.Summary{Sum: decimal.Zero}
	for _, e := range r.match(s, f) {
		t, ok := byCategory[e.Category]
		if !ok {
			t = &ledger.CategoryTotal{Category: e.Category, Sum: decimal.Zero}
			byCategory[e.Category] = t
		}
		t.Sum = t.Sum.Add(e.Amount)
		t.Count++
		sum.Sum = sum.Sum.Add(e.Amount)
		sum.Count++
	}
	for _, t := range byCategory {
		sum.Items = append(sum.Items, *t)
	}
	slices.SortFunc(sum.Items, func(a, b ledger.CategoryTotal) int {
		return cmp.Or(b.Sum.Cmp(a.Sum), cmp.Compare(a.Category, b.Category))
	})
	return sum, nil
}

func (r *OperationRepository) Aggregate(_ context.Context, s ledger.Scope, f ledger.Filter, b ledger.Bucket) ([]ledger.BucketTotal, error) {
	type key struct {
		start time.Time
		c     ledger.Category
	}
	totals := map[key]*ledger.BucketTotal{}
	var order []key
	for _, e := range r.match(s, f) {
		k := key{ledger.TruncateDate(e.Date, b), e.Category}
		t, ok := totals[k]
		if !ok {
			t = &ledger.BucketTotal{Start: k.start, Category: k.c, Sum: decimal.Zero}
			totals[k] = t
			order = append(order, k)
		}
		t.Sum = t.Sum.Add(e.Amount)
		t.Count++
	}
	out := make([]ledger.BucketTotal, len(order))
	for i, k := range order {
		out[i] = *totals[k]
	}
	return out, nil
}

// All returns every stored expense ordered by ID, regardless of scope.
func (r *OperationRepository) All() []ledger.Operation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ledger.Operation, 0, len(r.items))
	for _, e := range r.items {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b ledger.Operation) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (r *OperationRepository) match(s ledger.Scope, f ledger.Filter) []ledger.Operation {
	r.mu.Lock()
	defer r.mu.Unlock()
	query := strings.ToLower(f.Query)
	var out []ledger.Operation
	for _, e := range r.items {
		switch {
		case !r.visible(s, e),
			f.UserID != nil && e.UserID != *f.UserID,
			f.Category != nil && e.Category != *f.Category,
			f.From != nil && e.Date.Before(*f.From),
			f.To != nil && e.Date.After(*f.To),
			query != "" && !strings.Contains(strings.ToLower(e.Comment), query):
			continue
		}
		out = append(out, e)
	}
	return out
}

// visible applies the scope like the SQL repository: the zero scope sees nothing.
func (r *OperationRepository) visible(s ledger.Scope, e ledger.Operation) bool {
	if s.FamilyID() != 0 {
		return r.families[e.UserID] == s.FamilyID()
	}
	return e.UserID == s.UserID()
}

// deleteOwner drops a deleted user's expenses, like ON DELETE CASCADE.
func (r *OperationRepository) deleteOwner(userID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.items {
		if e.UserID == userID {
			delete(r.items, id)
		}
	}
	delete(r.families, userID)
}

// SetFamily records the family of a user for InFamily scopes; users without
// one are in no family.
func (r *OperationRepository) SetFamily(userID, familyID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.families[userID] = familyID
}
