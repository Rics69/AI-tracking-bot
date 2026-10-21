// Package agent turns free-form messages into operations on the user's
// expenses and incomes: the LLM decides which tool to call, the agent
// executes it for the current user.
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
	UserID   int64 // Telegram user ID: the only source of the expense owner
	Name     string
	Location *time.Location // the user's timezone; nil means Config.Location
}

// Clock tells the agent what "today" is in a timezone.
type Clock interface {
	TodayIn(loc *time.Location) time.Time
}

// UsageRecorder accounts what a reply cost: the bot keeps per-user daily limits.
type UsageRecorder interface {
	RecordUsage(ctx context.Context, userID int64, day time.Time, u llm.Usage, failed bool)
}

// Config tunes the agent.
type Config struct {
	Location *time.Location // default timezone of users
	MaxSteps int
	Usage    UsageRecorder // optional
}

// Agent is the expense assistant.
type Agent struct {
	llm      llm.Client
	ledger   Ledger
	clock    Clock
	history  HistoryStore
	loc      *time.Location
	maxSteps int
	usage    UsageRecorder
	log      *slog.Logger
}

// New creates an agent.
func New(client llm.Client, ledger Ledger, clock Clock, history HistoryStore, cfg Config, log *slog.Logger) *Agent {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	return &Agent{
		llm: client, ledger: ledger, clock: clock, history: history,
		loc: cfg.Location, maxSteps: cfg.MaxSteps, usage: cfg.Usage, log: log,
	}
}

// Reset forgets the conversation with the caller.
func (a *Agent) Reset(ctx context.Context, userID int64) {
	if err := a.history.Reset(ctx, userID); err != nil {
		a.log.WarnContext(ctx, "reset history", "user_id", userID, "err", err)
	}
}

// Reply handles one user message and returns the text to send back.
func (a *Agent) Reply(ctx context.Context, caller Caller, text string) (string, error) {
	start := time.Now()
	userMsg := llm.Message{Role: llm.RoleUser, Content: text}

	loc := caller.Location
	if loc == nil {
		loc = a.loc
	}
	today := a.clock.TodayIn(loc)
	t := turn{userID: caller.UserID, today: today}
	tools := toolDefs(today)
	messages := []llm.Message{{Role: llm.RoleSystem, Content: systemPrompt(today, loc, caller.Name)}}
	// Without history the agent still works, only follow-ups lose context:
	// a storage hiccup must not cost the user their reply.
	history, err := a.history.Messages(ctx, caller.UserID)
	if err != nil {
		a.log.WarnContext(ctx, "load history", "user_id", caller.UserID, "err", err)
	}
	messages = append(messages, history...)
	messages = append(messages, userMsg)
	turn := []llm.Message{userMsg}

	var usage llm.Usage
	account := func(failed bool) {
		if a.usage != nil {
			// The request may have timed out; the accounting must still be saved.
			a.usage.RecordUsage(context.WithoutCancel(ctx), caller.UserID, today, usage, failed)
		}
	}
	for step := 0; step < a.maxSteps; step++ {
		resp, err := a.llm.Chat(ctx, llm.Request{
			Messages:  messages,
			Tools:     tools,
			SessionID: fmt.Sprintf("tg-%d", caller.UserID),
		})
		if errors.Is(err, llm.ErrBlocked) {
			account(false)
			return replyBlocked, nil
		}
		if err != nil {
			account(true)
			return "", fmt.Errorf("llm: %w", err)
		}
		addUsage(&usage, resp.Usage)

		msg := resp.Message
		messages = append(messages, msg)
		turn = append(turn, msg)

		if msg.ToolCall == nil {
			if err := a.history.Append(ctx, caller.UserID, turn); err != nil {
				a.log.WarnContext(ctx, "save history", "user_id", caller.UserID, "err", err)
			}
			a.log.InfoContext(ctx, "agent reply",
				"user_id", caller.UserID, "steps", step+1, "duration", time.Since(start).Round(time.Millisecond),
				"prompt_tokens", usage.PromptTokens, "cached_tokens", usage.CachedTokens,
				"completion_tokens", usage.CompletionTokens)
			account(false)
			if strings.TrimSpace(msg.Content) == "" {
				return replyNoAnswer, nil
			}
			return msg.Content, nil
		}

		result, err := a.runTool(ctx, t, msg.ToolCall)
		if err != nil {
			account(true)
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
	account(false)
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
