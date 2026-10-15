// Package agent turns free-form messages into expense operations: the LLM
// decides which tool to call, the agent executes it for the current user.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

const (
	// DefaultMaxSteps bounds model calls per message: e.g. find + update + answer is 3.
	DefaultMaxSteps = 6

	replyGaveUp   = "Не получилось разобрать запрос. Попробуйте сформулировать иначе, например: «кофе 350» или «сколько потратил за неделю»."
	replyBlocked  = "Не могу ответить на это. Я помогаю вести учёт трат — например, напишите «кофе 350»."
	replyNoAnswer = "Готово."
)

// Caller is the Telegram user the agent acts for.
type Caller struct {
	UserID int64 // Telegram user ID: the only source of the expense owner
	Name   string
}

// Clock tells the agent what "today" is in the family timezone.
type Clock interface {
	Today() time.Time
}

// Config tunes the agent.
type Config struct {
	Location *time.Location
	MaxSteps int
}

// Agent is the expense assistant.
type Agent struct {
	llm      llm.Client
	expenses Expenses
	clock    Clock
	history  *History
	loc      *time.Location
	maxSteps int
	log      *slog.Logger
}

// New creates an agent.
func New(client llm.Client, expenses Expenses, clock Clock, history *History, cfg Config, log *slog.Logger) *Agent {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	return &Agent{
		llm: client, expenses: expenses, clock: clock, history: history,
		loc: cfg.Location, maxSteps: cfg.MaxSteps, log: log,
	}
}

// Reset forgets the conversation with the caller.
func (a *Agent) Reset(userID int64) { a.history.Reset(userID) }

// Reply handles one user message and returns the text to send back.
func (a *Agent) Reply(ctx context.Context, caller Caller, text string) (string, error) {
	start := time.Now()
	userMsg := llm.Message{Role: llm.RoleUser, Content: text}

	messages := []llm.Message{{Role: llm.RoleSystem, Content: systemPrompt(a.clock.Today(), a.loc, caller.Name)}}
	messages = append(messages, a.history.Messages(caller.UserID)...)
	messages = append(messages, userMsg)
	turn := []llm.Message{userMsg}

	var usage llm.Usage
	for step := 0; step < a.maxSteps; step++ {
		resp, err := a.llm.Chat(ctx, llm.Request{
			Messages:  messages,
			Tools:     tools,
			SessionID: fmt.Sprintf("tg-%d", caller.UserID),
		})
		if errors.Is(err, llm.ErrBlocked) {
			return replyBlocked, nil
		}
		if err != nil {
			return "", fmt.Errorf("llm: %w", err)
		}
		addUsage(&usage, resp.Usage)

		msg := resp.Message
		messages = append(messages, msg)
		turn = append(turn, msg)

		if msg.ToolCall == nil {
			a.history.Append(caller.UserID, turn)
			a.log.InfoContext(ctx, "agent reply",
				"user_id", caller.UserID, "steps", step+1, "duration", time.Since(start).Round(time.Millisecond),
				"prompt_tokens", usage.PromptTokens, "cached_tokens", usage.CachedTokens,
				"completion_tokens", usage.CompletionTokens)
			if strings.TrimSpace(msg.Content) == "" {
				return replyNoAnswer, nil
			}
			return msg.Content, nil
		}

		result, err := a.runTool(ctx, caller.UserID, msg.ToolCall)
		if err != nil {
			return "", fmt.Errorf("tool %s: %w", msg.ToolCall.Name, err)
		}
		a.log.InfoContext(ctx, "tool call",
			"user_id", caller.UserID, "tool", msg.ToolCall.Name,
			"args", string(msg.ToolCall.Arguments), "result", truncate(result, 300))

		toolMsg := llm.Message{Role: llm.RoleTool, ToolName: msg.ToolCall.Name, Content: result}
		messages = append(messages, toolMsg)
		turn = append(turn, toolMsg)
	}

	// The model kept calling tools without answering. The unfinished turn is
	// not saved so it does not confuse the next request.
	a.log.WarnContext(ctx, "agent gave up", "user_id", caller.UserID, "steps", a.maxSteps)
	return replyGaveUp, nil
}

func addUsage(total *llm.Usage, u llm.Usage) {
	total.PromptTokens += u.PromptTokens
	total.CompletionTokens += u.CompletionTokens
	total.CachedTokens += u.CachedTokens
	total.TotalTokens += u.TotalTokens
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
