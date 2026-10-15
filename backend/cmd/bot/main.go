// Command bot runs the Telegram bot with the expense agent (long polling).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-telegram/bot"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm/provider"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/logger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/postgres"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bot:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadBot()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	client, err := provider.New(cfg.LLM, log)
	if err != nil {
		return err
	}
	expenses := expense.NewService(postgres.NewExpenseRepository(pool), cfg.Location)
	assistant := agent.New(client, expenses, expenses,
		agent.NewHistory(cfg.HistoryTurns, cfg.HistoryTTL),
		agent.Config{Location: cfg.Location, MaxSteps: cfg.MaxSteps},
		log.With("component", "agent"))
	handler := telegram.NewHandler(assistant, postgres.NewUserRepository(pool),
		cfg.AllowedUserIDs, cfg.ReplyTimeout, log.With("component", "telegram"))

	b, err := bot.New(cfg.TelegramToken,
		bot.WithDefaultHandler(handler.Handle),
		bot.WithErrorsHandler(func(err error) { log.Error("telegram polling", "err", err) }),
	)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}

	log.Info("bot started", "llm", cfg.Provider, "model", cfg.GigaChat.Model, "allowed_users", len(cfg.AllowedUserIDs))
	b.Start(ctx) // blocks until ctx is cancelled
	log.Info("bot stopped")
	return nil
}
