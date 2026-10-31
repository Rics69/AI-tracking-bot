package memory

import (
	"context"
	"sync"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
)

// Event is one recorded event.
type Event struct {
	UserID int64
	Name   string
	Props  events.Props
}

// Events implements events.Store in memory.
type Events struct {
	mu    sync.Mutex
	items []Event
}

var _ events.Store = (*Events)(nil)

func NewEvents() *Events { return &Events{} }

func (r *Events) Add(_ context.Context, userID int64, name string, props events.Props) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, Event{UserID: userID, Name: name, Props: props})
	return nil
}

// Named returns the recorded events with the given name.
func (r *Events) Named(name string) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for _, e := range r.items {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}
