// Command bot runs the Telegram bot with the expense agent (long polling).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-telegram/bot"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm/provider"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/logger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/metrics"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
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

	metrics.Serve(ctx, cfg.MetricsAddr, pool.Ping, log)

	client, err := provider.New(cfg.LLM, log)
	if err != nil {
		return err
	}
	expenses := expense.NewService(postgres.NewExpenseRepository(pool), cfg.Location)
	// History lives in PostgreSQL: a restart or redeploy does not cut dialogs short.
	history := postgres.NewChatHistoryRepository(pool, cfg.HistoryTurns, cfg.HistoryTTL)
	go every(ctx, time.Hour, func() {
		n, err := history.DeleteOlderThan(ctx, cfg.HistoryRetention)
		if err != nil {
			log.Warn("history cleanup", "err", err)
		} else if n > 0 {
			log.Info("history cleanup", "deleted_turns", n)
		}
	})
	assistant := agent.New(client, expenses, expenses, history,
		agent.Config{Location: cfg.Location, MaxSteps: cfg.MaxSteps},
		log.With("component", "agent"))
	members := family.NewService(postgres.NewUserRepository(pool), postgres.NewInviteRepository(pool),
		postgres.NewSettingsRepository(pool), cfg.AllowedUserIDs)
	// ALLOWED_TELEGRAM_IDS only seeds the family heads; later members join by invite.
	if err := members.Bootstrap(ctx, cfg.AllowedUserIDs); err != nil {
		return err
	}
	handler := telegram.NewHandler(assistant, members, cfg.ReplyTimeout, log.With("component", "telegram"))

	b, err := bot.New(cfg.TelegramToken,
		bot.WithDefaultHandler(handler.Handle),
		bot.WithErrorsHandler(func(err error) { log.Error("telegram polling", "err", err) }),
	)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	// The panel builds invite links t.me/<username>?start=<code>.
	me, err := b.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram getMe: %w", err)
	}
	if err := members.SetBotUsername(ctx, me.Username); err != nil {
		return err
	}

	// Monday reports go to private chats, whose ID equals the user's Telegram ID.
	weekly := report.NewWeekly(expenses, postgres.NewUserRepository(pool), postgres.NewReportRunRepository(pool),
		func(ctx context.Context, chatID int64, text string) error {
			err := handler.Send(ctx, b, chatID, text)
			if errors.Is(err, telegram.ErrUndeliverable) {
				return fmt.Errorf("%w: %w", report.ErrUndeliverable, err)
			}
			return err
		},
		cfg.Location, log.With("component", "report"))
	go weekly.Run(ctx)

	log.Info("bot started", "username", me.Username, "llm", cfg.Provider, "model", cfg.GigaChat.Model,
		"family_heads", len(cfg.AllowedUserIDs), "open_family", len(cfg.AllowedUserIDs) == 0)
	b.Start(ctx) // blocks until ctx is cancelled
	log.Info("bot stopped")
	return nil
}

// every runs fn now and then at each interval until ctx is cancelled.
func every(ctx context.Context, interval time.Duration, fn func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		fn()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
