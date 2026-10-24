// Command bot runs the Telegram bot with the expense agent (long polling).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm/provider"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/logger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/postgres"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/telegram"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/texts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/usage"
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
	expenses := ledger.NewService(postgres.NewOperationRepository(pool), cfg.Location)
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
	meter := usage.NewMeter(postgres.NewUsageRepository(pool),
		usage.Limits{Messages: cfg.UserDailyMessages, Tokens: cfg.UserDailyTokens}, cfg.AdminID, log)
	budgets := budget.NewService(postgres.NewBudgetRepository(pool), expenses)
	assistant := agent.New(client, expenses, expenses, history,
		agent.Config{Location: cfg.Location, MaxSteps: cfg.MaxSteps, Usage: meter, Budget: budgets},
		log.With("component", "agent"))
	users := postgres.NewUserRepository(pool)
	members := family.NewService(users, postgres.NewInviteRepository(pool), postgres.NewSettingsRepository(pool))
	botTexts, err := texts.Load()
	if err != nil {
		return err
	}
	handler := telegram.NewHandler(assistant, members, meter, telegram.Config{
		ReplyTimeout: cfg.ReplyTimeout,
		Location:     cfg.Location,
		AdminID:      cfg.AdminID,
		PrivacyURL:   cfg.PrivacyURL,
		MiniAppURL:   cfg.MiniAppURL,
		Texts:        botTexts,
		Stats:        postgres.NewStatsRepository(pool),
	}, log.With("component", "telegram"))

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
	if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: telegram.Commands(botTexts, cfg.MiniAppURL != "")}); err != nil {
		log.Warn("set bot commands", "err", err) // the bot works without the menu
	}
	if cfg.AdminID != 0 {
		// The owner's chat also lists /stats, /ban and /unban.
		if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
			Commands: telegram.AdminCommands(botTexts, cfg.MiniAppURL != ""),
			Scope:    &models.BotCommandScopeChat{ChatID: cfg.AdminID},
		}); err != nil {
			log.Warn("set admin commands", "err", err)
		}
		// Five failed replies in a row mean GigaChat is down: tell the owner, at most hourly.
		meter.OnOutage(5, time.Hour, func(ctx context.Context, failures int) {
			text := texts.Fill(botTexts.LLMAlert, "count", strconv.Itoa(failures))
			if err := handler.Send(ctx, b, cfg.AdminID, text); err != nil {
				log.Error("llm outage alert", "err", err)
			}
		})
	}
	if menu := telegram.MenuButton(botTexts, cfg.MiniAppURL); menu != nil {
		if _, err := b.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{MenuButton: menu}); err != nil {
			log.Warn("set menu button", "err", err)
		}
	} else {
		log.Info("MINI_APP_URL is not set: no mini app button in the bot")
	}

	// Reports and reminders go to private chats, whose ID equals the user's Telegram ID.
	undeliverable := func(err error) error {
		if errors.Is(err, telegram.ErrUndeliverable) {
			return fmt.Errorf("%w: %w", report.ErrUndeliverable, err)
		}
		return err
	}
	runs := postgres.NewNotificationRunRepository(pool)
	weekly := report.NewWeekly(expenses, users, runs,
		func(ctx context.Context, chatID int64, text string) error {
			return undeliverable(handler.Send(ctx, b, chatID, text))
		},
		cfg.Location, log.With("component", "report"))
	go weekly.Run(ctx)
	daily := report.NewDaily(budgets, users, runs,
		func(ctx context.Context, chatID int64, text string) error {
			return undeliverable(handler.SendWithApp(ctx, b, chatID, text))
		},
		botTexts, cfg.Location, log.With("component", "reminder"))
	go daily.Run(ctx)

	if cfg.AdminID == 0 {
		log.Warn("ADMIN_TELEGRAM_ID is not set: feedback is only logged")
	}
	log.Info("bot started", "username", me.Username, "llm", cfg.Provider, "model", cfg.GigaChat.Model,
		"daily_messages", cfg.UserDailyMessages, "daily_tokens", cfg.UserDailyTokens)
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
