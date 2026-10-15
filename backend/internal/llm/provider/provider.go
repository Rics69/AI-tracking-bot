// Package provider builds the configured llm.Client.
package provider

import (
	"fmt"
	"log/slog"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm/gigachat"
)

// New returns the client selected by LLM_PROVIDER.
func New(cfg config.LLM, log *slog.Logger) (llm.Client, error) {
	switch cfg.Provider {
	case "gigachat":
		g := cfg.GigaChat
		return gigachat.New(gigachat.Config{
			AuthKey:            g.AuthKey,
			Scope:              g.Scope,
			Model:              g.Model,
			BaseURL:            g.BaseURL,
			AuthURL:            g.AuthURL,
			Temperature:        g.Temperature,
			Timeout:            g.Timeout,
			MaxRetries:         g.MaxRetries,
			MaxConcurrent:      g.MaxConcurrent,
			InsecureSkipVerify: g.InsecureSkipVerify,
		}, log.With("llm", "gigachat"))
	default:
		return nil, fmt.Errorf("unsupported LLM provider %q", cfg.Provider)
	}
}
