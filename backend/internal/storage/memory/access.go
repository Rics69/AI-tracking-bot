package memory

import (
	"context"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
)

// Access implements access.Store in memory.
type Access struct {
	mu     sync.Mutex
	items  []access.Entitlement
	promos map[string]*promoState
}

type promoState struct {
	access.Promo
	users map[int64]bool
}

var _ access.Store = (*Access)(nil)

func NewAccess() *Access { return &Access{promos: map[string]*promoState{}} }

func (r *Access) Add(_ context.Context, e access.Entitlement) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, e)
	return nil
}

func (r *Access) Until(_ context.Context, userID int64, product string) (*time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var until *time.Time
	for _, e := range r.items {
		if e.UserID == userID && e.Product == product && (until == nil || e.ExpiresAt.After(*until)) {
			end := e.ExpiresAt
			until = &end
		}
	}
	return until, nil
}

func (r *Access) Close(_ context.Context, userID int64, product string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.items {
		if e.UserID == userID && e.Product == product && e.ExpiresAt.After(at) {
			r.items[i].ExpiresAt = at
		}
	}
	return nil
}

func (r *Access) Endings(_ context.Context, product string, from time.Time) ([]access.Ending, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	latest := map[int64]time.Time{}
	for _, e := range r.items {
		if e.Product == product && e.ExpiresAt.After(latest[e.UserID]) {
			latest[e.UserID] = e.ExpiresAt
		}
	}
	var out []access.Ending
	for id, at := range latest {
		if !at.Before(from) {
			out = append(out, access.Ending{UserID: id, At: at})
		}
	}
	return out, nil
}

func (r *Access) Active(_ context.Context, product string, at time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	users := map[int64]bool{}
	for _, e := range r.items {
		if e.Product == product && e.ExpiresAt.After(at) {
			users[e.UserID] = true
		}
	}
	return len(users), nil
}

func (r *Access) AddPromo(_ context.Context, p access.Promo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.promos[string(p.Hash)] = &promoState{Promo: p, users: map[int64]bool{}}
	return nil
}

func (r *Access) RedeemPromo(_ context.Context, hash []byte, userID int64, at time.Time) (access.Promo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.promos[string(hash)]
	if !ok {
		return access.Promo{}, access.ErrPromoInvalid
	}
	if p.users[userID] {
		return access.Promo{}, access.ErrPromoUsed
	}
	if len(p.users) >= p.MaxUses || p.ExpiresAt != nil && !p.ExpiresAt.After(at) {
		return access.Promo{}, access.ErrPromoInvalid
	}
	p.users[userID] = true
	return p.Promo, nil
}
