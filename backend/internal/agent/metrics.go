package agent

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	repliesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_replies_total",
		Help: "Replies to user messages by outcome: ok, gave_up (step limit), blocked (provider refused), error.",
	}, []string{"outcome"})

	replyDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "agent_reply_duration_seconds",
		Help:    "Time from a user message to the agent's reply, all model and tool calls included.",
		Buckets: []float64{0.5, 1, 2, 3, 5, 8, 13, 20, 30, 60},
	})

	replySteps = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "agent_reply_steps",
		Help:    "Model calls needed for one reply: 2 is a typical add, 3 a find-then-update.",
		Buckets: []float64{1, 2, 3, 4, 5, 6},
	})

	toolCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_tool_calls_total",
		Help: "Tool calls by result: ok, tool_error (bad arguments sent back to the model), failure (infrastructure).",
	}, []string{"tool", "result"})
)

// Outcomes for agent_replies_total.
const (
	outcomeOK      = "ok"
	outcomeGaveUp  = "gave_up"
	outcomeBlocked = "blocked"
	outcomeError   = "error"
)

func observeReply(outcome string, steps int, d time.Duration) {
	repliesTotal.WithLabelValues(outcome).Inc()
	replyDuration.Observe(d.Seconds())
	if steps > 0 {
		replySteps.Observe(float64(steps))
	}
}

// knownTool keeps the tool label bounded: a model inventing names must not
// create new time series.
func knownTool(name string) string {
	switch name {
	case toolAdd, toolFind, toolSummary, toolUpdate, toolDelete:
		return name
	default:
		return "unknown"
	}
}
