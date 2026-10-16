package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// ChatHistoryRepository implements agent.HistoryStore in PostgreSQL with the
// same rules as the in-memory store: the last maxTurns turns, and a
// conversation idle for longer than ttl starts from scratch.
type ChatHistoryRepository struct {
	pool     *pgxpool.Pool
	maxTurns int
	ttl      time.Duration
}

var _ agent.HistoryStore = (*ChatHistoryRepository)(nil)

func NewChatHistoryRepository(pool *pgxpool.Pool, maxTurns int, ttl time.Duration) *ChatHistoryRepository {
	return &ChatHistoryRepository{pool: pool, maxTurns: maxTurns, ttl: ttl}
}

func (r *ChatHistoryRepository) Messages(ctx context.Context, userID int64) ([]llm.Message, error) {
	rows, err := r.pool.Query(ctx, `
		WITH recent AS (
			SELECT id, messages, created_at FROM chat_turns
			WHERE user_id = $1 ORDER BY id DESC LIMIT $2
		)
		SELECT messages FROM recent
		WHERE (SELECT max(created_at) FROM recent) > now() - make_interval(secs => $3)
		ORDER BY id`, userID, r.maxTurns, r.ttl.Seconds())
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	turns, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, fmt.Errorf("scan history: %w", err)
	}
	var out []llm.Message
	for _, raw := range turns {
		var stored []storedMessage
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, fmt.Errorf("decode history: %w", err)
		}
		for _, m := range stored {
			out = append(out, m.toLLM())
		}
	}
	return out, nil
}

func (r *ChatHistoryRepository) Append(ctx context.Context, userID int64, turn []llm.Message) error {
	stored := make([]storedMessage, len(turn))
	for i, m := range turn {
		stored[i] = fromLLM(m)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO chat_turns (user_id, messages) VALUES ($1, $2)`, userID, raw); err != nil {
		return fmt.Errorf("save history: %w", err)
	}
	return nil
}

func (r *ChatHistoryRepository) Reset(ctx context.Context, userID int64) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM chat_turns WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("reset history: %w", err)
	}
	return nil
}

// DeleteOlderThan removes turns older than age and returns how many: dialogs
// are personal data and are kept only as long as useful for debugging.
func (r *ChatHistoryRepository) DeleteOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM chat_turns WHERE created_at < now() - make_interval(secs => $1)`, age.Seconds())
	if err != nil {
		return 0, fmt.Errorf("clean up history: %w", err)
	}
	return tag.RowsAffected(), nil
}

// storedMessage is the JSON form of llm.Message, decoupled from the domain type
// so renaming a Go field does not break history already in the database.
type storedMessage struct {
	Role     string          `json:"role"`
	Content  string          `json:"content,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	StateID  string          `json:"state_id,omitempty"`
	ToolCall *storedToolCall `json:"tool_call,omitempty"`
}

type storedToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func fromLLM(m llm.Message) storedMessage {
	s := storedMessage{Role: string(m.Role), Content: m.Content, ToolName: m.ToolName, StateID: m.StateID}
	if m.ToolCall != nil {
		s.ToolCall = &storedToolCall{ID: m.ToolCall.ID, Name: m.ToolCall.Name, Arguments: m.ToolCall.Arguments}
	}
	return s
}

func (s storedMessage) toLLM() llm.Message {
	m := llm.Message{Role: llm.Role(s.Role), Content: s.Content, ToolName: s.ToolName, StateID: s.StateID}
	if s.ToolCall != nil {
		m.ToolCall = &llm.ToolCall{ID: s.ToolCall.ID, Name: s.ToolCall.Name, Arguments: s.ToolCall.Arguments}
	}
	return m
}
