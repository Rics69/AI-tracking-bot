package memory

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/flow"
)

// Flows implements flow.Store in memory.
type Flows struct {
	mu    sync.Mutex
	items map[int64]flow.State
	Now   func() time.Time // the clock of UpdatedAt; nil: time.Now
}

var _ flow.Store = (*Flows)(nil)

func NewFlows() *Flows { return &Flows{items: map[int64]flow.State{}} }

func (r *Flows) Get(_ context.Context, userID int64) (flow.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.items[userID]
	if !ok {
		return flow.State{}, flow.ErrNone
	}
	s.Data = maps.Clone(s.Data)
	return s, nil
}

func (r *Flows) Save(_ context.Context, s flow.State) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.Data = maps.Clone(s.Data)
	s.UpdatedAt = time.Now()
	if r.Now != nil {
		s.UpdatedAt = r.Now()
	}
	r.items[s.UserID] = s
	return nil
}

func (r *Flows) Delete(_ context.Context, userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, userID)
	return nil
}
