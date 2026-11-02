// Command bot runs the Telegram bot with the expense agent (long polling).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/access"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/accounts"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/budget"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/card"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/events"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/flow"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/game"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm/provider"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/logger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/quick"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/rates"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/report"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/postgres"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/tasks"
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
	rateStore := postgres.NewRateRepository(pool)
	money := accounts.NewService(postgres.NewAccountRepository(pool), rateStore)
	expenses.Rates, expenses.Accounts = rateStore, money
	plans := tasks.NewService(postgres.NewTaskRepository(pool))
	// History lives in PostgreSQL: a restart or redeploy does not cut dialogs short.
	history := postgres.NewChatHistoryRepository(pool, cfg.HistoryTurns, cfg.HistoryTTL)
	flows := postgres.NewFlowRepository(pool)
	go every(ctx, time.Hour, func() {
		n, err := history.DeleteOlderThan(ctx, cfg.HistoryRetention)
		if err != nil {
			log.Warn("history cleanup", "err", err)
		} else if n > 0 {
			log.Info("history cleanup", "deleted_turns", n)
		}
		if _, err := flows.DeleteOlderThan(ctx, flow.TTL); err != nil {
			log.Warn("flow cleanup", "err", err)
		}
	})
	meter := usage.NewMeter(postgres.NewUsageRepository(pool),
		usage.Limits{Messages: cfg.UserDailyMessages, Tokens: cfg.UserDailyTokens}, cfg.AdminID, log)
	packageSince, _ := cfg.PackageStart() // validated by LoadBot
	meter.SetPackage(usage.Package{Tokens: cfg.LLMTokenPackage, Since: packageSince, Daily: cfg.LLMDailyTokens})
	tracker := events.NewTracker(postgres.NewEventRepository(pool), log.With("component", "events"))
	defer tracker.Wait()
	budgets := budget.NewService(postgres.NewBudgetRepository(pool), expenses)
	records, err := quick.New(expenses, budgets)
	if err != nil {
		return err
	}
	assistant := agent.New(client, expenses, expenses, history,
		agent.Config{Location: cfg.Location, MaxSteps: cfg.MaxSteps, Usage: meter, Budget: budgets, Accounts: money, Tasks: plans, Events: tracker},
		log.With("component", "agent"))
	users := postgres.NewUserRepository(pool)
	members := family.NewService(users, postgres.NewInviteRepository(pool), postgres.NewSettingsRepository(pool))
	botTexts, err := texts.Load()
	if err != nil {
		return err
	}
	catalog, err := game.LoadCatalog()
	if err != nil {
		return err
	}
	club := access.New(postgres.NewAccessRepository(pool), postgres.NewSettingsRepository(pool),
		usage.Limits{Messages: cfg.UserDailyMessages, Tokens: cfg.UserDailyTokens},
		usage.Limits{Messages: cfg.ClubMessages, Tokens: cfg.ClubTokens})
	meter.UseTiers(club.Limits)
	play := game.NewService(catalog, postgres.NewGameRepository(pool), expenses, budgets)
	play.Access = club
	handler := telegram.NewHandler(assistant, members, meter, telegram.Config{
		ReplyTimeout: cfg.ReplyTimeout,
		Location:     cfg.Location,
		AdminID:      cfg.AdminID,
		PrivacyURL:   cfg.PrivacyURL,
		MiniAppURL:   cfg.MiniAppURL,
		Texts:        botTexts,
		Stats:        postgres.NewStatsRepository(pool),
		Quick:        records,
		Events:       tracker,
		Flows:        flows,
		Budget:       budgets,
		Game:         play,
		Tasks:        plans,
		Access:       club,
		ClubChats:    cfg.ClubChats,

		PackageTokens: cfg.LLMTokenPackage,
		PackageSince:  packageSince,
	}, log.With("component", "telegram"))

	opts := []bot.Option{
		bot.WithDefaultHandler(handler.Handle),
		bot.WithErrorsHandler(func(err error) { log.Error("telegram polling", "err", err) }),
	}
	if proxy, _ := cfg.TelegramProxyURL(); proxy != nil { // validated by LoadBot
		// Only the Telegram client goes through the proxy; GigaChat stays direct.
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(proxy)
		opts = append(opts, bot.WithHTTPClient(telegramPollTimeout, &http.Client{Transport: transport, Timeout: telegramPollTimeout}))
		log.Info("telegram through a proxy", "proxy", proxy.Redacted())
	}
	b, err := bot.New(cfg.TelegramToken, opts...)
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
	handler.SetBotUsername(me.Username)
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
		if cfg.LLMTokenPackage > 0 {
			alert := report.NewPackageAlert(meter, postgres.NewNotificationRunRepository(pool),
				func(ctx context.Context, chatID int64, text string) error { return handler.Send(ctx, b, chatID, text) },
				cfg.AdminID, cfg.LLMTokenPackage, packageSince, botTexts, log.With("component", "package"))
			go every(ctx, 10*time.Minute, func() { alert.Tick(ctx) })
		}
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
	weekly.Facts, weekly.Texts = play, botTexts
	go weekly.Run(ctx)
	updater := rates.NewUpdater(rateStore, log.With("component", "rates"))
	go func() {
		updater.Backfill(ctx, expenses.Today())
		every(ctx, time.Hour, func() { updater.Update(ctx) })
	}()
	sendRows := func(ctx context.Context, chatID int64, text string, rows [][]game.Action) error {
		return undeliverable(handler.SendActions(ctx, b, chatID, text, fromGame, rows))
	}
	sendCard := func(ctx context.Context, chatID int64, c card.Card, caption string, rows [][]game.Action) error {
		return undeliverable(handler.SendCard(ctx, b, chatID, c, caption, rows))
	}
	milestones := report.NewGoalProgress(budgets, users, runs,
		func(ctx context.Context, chatID int64, text string) error {
			return undeliverable(handler.SendWithApp(ctx, b, chatID, text, fromGoal))
		},
		botTexts, log.With("component", "goals"))
	milestones.SendCard = sendCard
	go milestones.Run(ctx)
	go report.NewTaskReminders(plans, users, runs, sendRows, botTexts, cfg.Location, log.With("component", "tasks")).Run(ctx)
	sync := report.NewClubSync(club, runs,
		func(ctx context.Context, chatID int64, text string) error {
			return undeliverable(handler.Send(ctx, b, chatID, text))
		},
		func(ctx context.Context, userID int64) error { return handler.RemoveFromClub(ctx, b, userID) },
		botTexts, log.With("component", "club"))
	go every(ctx, time.Hour, func() { sync.Tick(ctx) })
	go report.NewGame(play, users, runs, sendRows, sendCard, botTexts, cfg.Location, log.With("component", "game")).Run(ctx)
	daily := report.NewDaily(budgets, users, runs, rateStore,
		func(ctx context.Context, chatID int64, text string) error {
			err := handler.SendWithApp(ctx, b, chatID, text, fromMorning)
			if err == nil {
				tracker.Track(ctx, chatID, events.MorningSent, nil)
			}
			return undeliverable(err)
		},
		botTexts, cfg.Location, log.With("component", "reminder"))
	daily.Extras = report.GameMorning{Game: play, Texts: botTexts}
	daily.Tasks = plans
	daily.SendRows = func(ctx context.Context, chatID int64, text string, rows [][]game.Action) error {
		err := handler.SendActions(ctx, b, chatID, text, fromMorning, rows)
		if err == nil {
			tracker.Track(ctx, chatID, events.MorningSent, nil)
		}
		return undeliverable(err)
	}
	go daily.Run(ctx)

	if cfg.AdminID == 0 {
		log.Warn("ADMIN_TELEGRAM_ID is not set: feedback is only logged")
	}
	log.Info("bot started", "username", me.Username, "llm", cfg.Provider, "model", cfg.GigaChat.Model,
		"daily_messages", cfg.UserDailyMessages, "daily_tokens", cfg.UserDailyTokens, "token_package", cfg.LLMTokenPackage)
	b.Start(ctx) // blocks until ctx is cancelled
	log.Info("bot stopped")
	return nil
}

// telegramPollTimeout is the long-polling timeout of the Telegram client, as
// the library's default one.
const telegramPollTimeout = time.Minute

// fromMorning marks the mini app button of the morning reminder: the app
// reports the click.
const fromMorning = "morning"

// fromGoal and fromGame mark the mini app button of a goal milestone and of
// the messages of the game.
const (
	fromGoal = "goal"
	fromGame = "game"
)

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
