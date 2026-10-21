package agent

import (
	"context"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

func historyTurn(text string) []llm.Message {
	return []llm.Message{{Role: llm.RoleUser, Content: text}, {Role: llm.RoleAssistant, Content: "ok"}}
}

func TestHistoryKeepsLastTurns(t *testing.T) {
	h := NewHistory(2, time.Hour)
	for _, s := range []string{"a", "b", "c"} {
		_ = h.Append(context.Background(), 1, historyTurn(s))
	}
	msgs, _ := h.Messages(context.Background(), 1)
	if len(msgs) != 4 || msgs[0].Content != "b" || msgs[2].Content != "c" {
		t.Errorf("messages = %+v", msgs)
	}
	if len(must(h.Messages(context.Background(), 2))) != 0 {
		t.Error("conversations leak between users")
	}
}

func TestHistoryExpires(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	h := NewHistory(5, 30*time.Minute)
	h.now = func() time.Time { return now }
	_ = h.Append(context.Background(), 1, historyTurn("a"))

	now = now.Add(29 * time.Minute)
	if len(must(h.Messages(context.Background(), 1))) == 0 {
		t.Fatal("expired too early")
	}
	now = now.Add(2 * time.Minute)
	if len(must(h.Messages(context.Background(), 1))) != 0 {
		t.Fatal("did not expire")
	}
}

func must(msgs []llm.Message, err error) []llm.Message {
	if err != nil {
		panic(err)
	}
	return msgs
}
