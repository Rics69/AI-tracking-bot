package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/shopspring/decimal"

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

func (r *Rates) On(_ context.Context, code string, date time.Time) (decimal.Decimal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *rates.Rate
	for i, it := range r.items {
		if it.Code != code {
			continue
		}
		switch {
		case best == nil:
			best = &r.items[i]
		case !it.Date.After(date) && (best.Date.After(date) || it.Date.After(best.Date)):
			best = &r.items[i]
		case it.Date.After(date) && best.Date.After(date) && it.Date.Before(best.Date):
			best = &r.items[i]
		}
	}
	if best == nil {
		return decimal.Zero, rates.ErrNoRate
	}
	return best.Value, nil
}

func (r *Rates) Now(ctx context.Context, code string) (decimal.Decimal, error) {
	return r.On(ctx, code, time.Now())
}

func (r *Rates) Dates(_ context.Context, code string, from time.Time) ([]time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []time.Time
	for _, it := range r.items {
		if it.Code == code && !it.Date.Before(from) {
			out = append(out, it.Date)
		}
	}
	return out, nil
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
