package agent

import (
	"sync"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// History keeps the recent turns of each conversation in memory, so follow-ups
// like "удали вторую" or answers to clarifying questions work.
//
// A turn is the user message plus everything the model and tools produced for
// it. Turns are stored and dropped whole: GigaChat rejects a history where a
// function call has no matching result.
type History struct {
	mu       sync.Mutex
	convs    map[int64]*conversation
	maxTurns int
	ttl      time.Duration
	now      func() time.Time
}

type conversation struct {
	turns   [][]llm.Message
	updated time.Time
}

// NewHistory keeps up to maxTurns turns per conversation; a conversation idle
// for longer than ttl starts from scratch.
func NewHistory(maxTurns int, ttl time.Duration) *History {
	return &History{convs: map[int64]*conversation{}, maxTurns: maxTurns, ttl: ttl, now: time.Now}
}

// Messages returns the stored messages of a conversation, oldest first.
func (h *History) Messages(key int64) []llm.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.convs[key]
	if !ok {
		return nil
	}
	if h.now().Sub(c.updated) > h.ttl {
		delete(h.convs, key)
		return nil
	}
	var out []llm.Message
	for _, t := range c.turns {
		out = append(out, t...)
	}
	return out
}

// Append stores a completed turn and evicts the oldest ones over the limit.
func (h *History) Append(key int64, turn []llm.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.evictExpired()
	c, ok := h.convs[key]
	if !ok {
		c = &conversation{}
		h.convs[key] = c
	}
	c.turns = append(c.turns, turn)
	if len(c.turns) > h.maxTurns {
		c.turns = c.turns[len(c.turns)-h.maxTurns:]
	}
	c.updated = h.now()
}

// Reset forgets a conversation.
func (h *History) Reset(key int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.convs, key)
}

// evictExpired keeps memory bounded by users who stopped writing. Caller holds mu.
func (h *History) evictExpired() {
	now := h.now()
	for k, c := range h.convs {
		if now.Sub(c.updated) > h.ttl {
			delete(h.convs, k)
		}
	}
}
