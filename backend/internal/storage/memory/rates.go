package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
)

// Rates implements rates.Store in memory.
type Rates struct {
	mu    sync.Mutex
	items []rates.Rate
}

var _ rates.Store = (*Rates)(nil)

func NewRates() *Rates { return &Rates{} }

func (r *Rates) Save(_ context.Context, items []rates.Rate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, items...)
	return nil
}

func (r *Rates) Latest(_ context.Context, codes []string) ([]rates.Rate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	latest := map[string]rates.Rate{}
	for _, it := range r.items {
		if slices.Contains(codes, it.Code) && !latest[it.Code].Date.After(it.Date) {
			latest[it.Code] = it
		}
	}
	out := make([]rates.Rate, 0, len(latest))
	for _, it := range latest {
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b rates.Rate) int { return -cmp.Compare(a.Code, b.Code) })
	return out, nil
}
