package telegram

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	messagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "telegram_messages_total",
		Help: "Incoming private messages by how they were handled.",
	}, []string{"result"})

	sendRetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "telegram_send_retries_total",
		Help: "Replies that needed another attempt to reach Telegram.",
	})

	sendFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "telegram_send_failures_total",
		Help: "Replies that never reached the user: the user saw silence.",
	})
)

// Results for telegram_messages_total.
const (
	msgAnswered      = "answered"
	msgAgentFailed   = "agent_failed"
	msgRefused       = "refused"
	msgJoined        = "joined"
	msgInviteInvalid = "invite_invalid"
	msgAlreadyMember = "already_member"
	msgHelp          = "help"
	msgIgnored       = "ignored" // groups, stickers, too long
	msgFailure       = "failure"
)

func countMessage(result string) { messagesTotal.WithLabelValues(result).Inc() }
