// Package memory is an in-memory expense.Repository. It mirrors the semantics
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

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
)

// ExpenseRepository implements expense.Repository in memory.
type ExpenseRepository struct {
	mu     sync.Mutex
	items  map[int64]expense.Expense
	nextID int64
	clock  time.Time // creation timestamps: strictly increasing, so "last added" is stable
}

var _ expense.Repository = (*ExpenseRepository)(nil)

func NewExpenseRepository() *ExpenseRepository {
	return &ExpenseRepository{items: map[int64]expense.Expense{}, clock: time.Now()}
}

func (r *ExpenseRepository) Create(_ context.Context, e expense.Expense) (expense.Expense, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.clock = r.clock.Add(time.Millisecond)
	e.ID = r.nextID
	e.CreatedAt, e.UpdatedAt = r.clock, r.clock
	r.items[e.ID] = e
	return e, nil
}

func (r *ExpenseRepository) Get(_ context.Context, s expense.Scope, id int64) (expense.Expense, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[id]
	if !ok || !visible(s, e) {
		return expense.Expense{}, expense.ErrNotFound
	}
	return e, nil
}

func (r *ExpenseRepository) Update(_ context.Context, s expense.Scope, e expense.Expense) (expense.Expense, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.items[e.ID]
	if !ok || !visible(s, old) {
		return expense.Expense{}, expense.ErrNotFound
	}
	r.clock = r.clock.Add(time.Millisecond)
	e.CreatedAt, e.UpdatedAt = old.CreatedAt, r.clock
	r.items[e.ID] = e
	return e, nil
}

func (r *ExpenseRepository) Delete(_ context.Context, s expense.Scope, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.items[id]
	if !ok || !visible(s, e) {
		return expense.ErrNotFound
	}
	delete(r.items, id)
	return nil
}

func (r *ExpenseRepository) List(_ context.Context, s expense.Scope, f expense.Filter) (expense.Page, error) {
	matched := r.match(s, f)
	switch f.Sort {
	case expense.SortByCreatedAt:
		slices.SortFunc(matched, func(a, b expense.Expense) int {
			return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
		})
	case expense.SortByAmount:
		slices.SortFunc(matched, func(a, b expense.Expense) int {
			return cmp.Or(b.Amount.Cmp(a.Amount), cmp.Compare(b.ID, a.ID))
		})
	default:
		slices.SortFunc(matched, func(a, b expense.Expense) int {
			return cmp.Or(b.SpentAt.Compare(a.SpentAt), cmp.Compare(b.ID, a.ID))
		})
	}
	page := expense.Page{Items: []expense.Expense{}, Total: len(matched), Sum: decimal.Zero}
	for _, e := range matched {
		page.Sum = page.Sum.Add(e.Amount)
	}
	if f.Offset < len(matched) {
		page.Items = matched[f.Offset:min(f.Offset+f.Limit, len(matched))]
	}
	return page, nil
}

func (r *ExpenseRepository) Summary(_ context.Context, s expense.Scope, f expense.Filter) (expense.Summary, error) {
	byCategory := map[expense.Category]*expense.CategoryTotal{}
	sum := expense.Summary{Sum: decimal.Zero}
	for _, e := range r.match(s, f) {
		t, ok := byCategory[e.Category]
		if !ok {
			t = &expense.CategoryTotal{Category: e.Category, Sum: decimal.Zero}
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
	slices.SortFunc(sum.Items, func(a, b expense.CategoryTotal) int {
		return cmp.Or(b.Sum.Cmp(a.Sum), cmp.Compare(a.Category, b.Category))
	})
	return sum, nil
}

func (r *ExpenseRepository) Aggregate(_ context.Context, s expense.Scope, f expense.Filter, b expense.Bucket) ([]expense.BucketTotal, error) {
	type key struct {
		start time.Time
		c     expense.Category
	}
	totals := map[key]*expense.BucketTotal{}
	var order []key
	for _, e := range r.match(s, f) {
		k := key{expense.TruncateDate(e.SpentAt, b), e.Category}
		t, ok := totals[k]
		if !ok {
			t = &expense.BucketTotal{Start: k.start, Category: k.c, Sum: decimal.Zero}
			totals[k] = t
			order = append(order, k)
		}
		t.Sum = t.Sum.Add(e.Amount)
		t.Count++
	}
	out := make([]expense.BucketTotal, len(order))
	for i, k := range order {
		out[i] = *totals[k]
	}
	return out, nil
}

// All returns every stored expense ordered by ID, regardless of scope.
func (r *ExpenseRepository) All() []expense.Expense {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]expense.Expense, 0, len(r.items))
	for _, e := range r.items {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b expense.Expense) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (r *ExpenseRepository) match(s expense.Scope, f expense.Filter) []expense.Expense {
	r.mu.Lock()
	defer r.mu.Unlock()
	query := strings.ToLower(f.Query)
	var out []expense.Expense
	for _, e := range r.items {
		switch {
		case !visible(s, e),
			f.UserID != nil && e.UserID != *f.UserID,
			f.Category != nil && e.Category != *f.Category,
			f.From != nil && e.SpentAt.Before(*f.From),
			f.To != nil && e.SpentAt.After(*f.To),
			query != "" && !strings.Contains(strings.ToLower(e.Comment), query):
			continue
		}
		out = append(out, e)
	}
	return out
}

// visible applies the scope like the SQL repository: the zero scope sees nothing.
func visible(s expense.Scope, e expense.Expense) bool {
	return s.All() || e.UserID == s.UserID()
}
