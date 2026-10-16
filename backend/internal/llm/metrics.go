package llm

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics shared by all providers. Labels have a fixed, small set of values:
// never put user IDs or message text into labels.
var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_requests_total",
		Help: "Chat completion calls by outcome: ok, blocked, rate_limited, server_error, client_error, network.",
	}, []string{"provider", "model", "outcome"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "llm_request_duration_seconds",
		Help: "Duration of a chat completion call, retries included.",
		// LLM calls take seconds, not milliseconds: default buckets would be useless.
		Buckets: []float64{0.25, 0.5, 1, 2, 3, 5, 8, 13, 20, 30, 60},
	}, []string{"provider", "model"})

	tokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_tokens_total",
		Help: "Tokens by type: prompt (billed), cached (served from the provider's prompt cache), completion.",
	}, []string{"provider", "model", "type"})

	retriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_retries_total",
		Help: "Retried calls by reason: rate_limited, server_error, network.",
	}, []string{"provider", "reason"})
)

// Outcomes of a call for llm_requests_total.
const (
	OutcomeOK          = "ok"
	OutcomeBlocked     = "blocked"
	OutcomeRateLimited = "rate_limited"
	OutcomeServerError = "server_error"
	OutcomeClientError = "client_error"
	OutcomeNetwork     = "network"
)

// ObserveCall records one chat completion call.
func ObserveCall(provider, model, outcome string, d time.Duration, u Usage) {
	requestsTotal.WithLabelValues(provider, model, outcome).Inc()
	requestDuration.WithLabelValues(provider, model).Observe(d.Seconds())
	if outcome != OutcomeOK {
		return
	}
	tokensTotal.WithLabelValues(provider, model, "prompt").Add(float64(u.PromptTokens))
	tokensTotal.WithLabelValues(provider, model, "cached").Add(float64(u.CachedTokens))
	tokensTotal.WithLabelValues(provider, model, "completion").Add(float64(u.CompletionTokens))
}

// ObserveRetry records a retried call.
func ObserveRetry(provider, reason string) {
	retriesTotal.WithLabelValues(provider, reason).Inc()
}
