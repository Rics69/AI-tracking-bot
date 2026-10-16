package agent

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

func TestToolMetrics(t *testing.T) {
	okBefore := testutil.ToFloat64(toolCallsTotal.WithLabelValues(toolAdd, "ok"))
	errBefore := testutil.ToFloat64(toolCallsTotal.WithLabelValues(toolAdd, "tool_error"))
	unknownBefore := testutil.ToFloat64(toolCallsTotal.WithLabelValues("unknown", "tool_error"))
	repliesBefore := testutil.ToFloat64(repliesTotal.WithLabelValues(outcomeOK))

	model := &scriptedLLM{responses: []llm.Response{
		call(toolAdd, `{"amount": 350, "category": "кофейня", "comment": "кофе", "date": "2026-10-16"}`),
		call("drop_database", `{}`),
		call(toolAdd, `{"amount": 350, "category": "food", "comment": "кофе", "date": "2026-10-16"}`),
		text("Записано"),
	}}
	if _, err := newTestAgent(model, newMemExpenses()).Reply(context.Background(), anna, "кофе 350"); err != nil {
		t.Fatal(err)
	}

	if d := testutil.ToFloat64(toolCallsTotal.WithLabelValues(toolAdd, "ok")) - okBefore; d != 1 {
		t.Errorf("ok calls += %v, want 1", d)
	}
	if d := testutil.ToFloat64(toolCallsTotal.WithLabelValues(toolAdd, "tool_error")) - errBefore; d != 1 {
		t.Errorf("tool errors += %v, want 1", d)
	}
	// Invented tool names collapse into one label value.
	if d := testutil.ToFloat64(toolCallsTotal.WithLabelValues("unknown", "tool_error")) - unknownBefore; d != 1 {
		t.Errorf("unknown tool calls += %v, want 1", d)
	}
	if d := testutil.ToFloat64(repliesTotal.WithLabelValues(outcomeOK)) - repliesBefore; d != 1 {
		t.Errorf("ok replies += %v, want 1", d)
	}
}
