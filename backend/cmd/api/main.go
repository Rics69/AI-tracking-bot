// Command api serves the REST API of the family head's web panel.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/httpapi"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/logger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadAPI()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log) // libraries that log via the standard logger end up in the same output

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
	log.Info("database ready")

	handler := httpapi.NewHandler(httpapi.Deps{
		Expenses: expense.NewService(postgres.NewExpenseRepository(pool), cfg.Location),
		// The API never admits anyone, so bootstrap IDs (open/closed family) do not matter here.
		Family: family.NewService(postgres.NewUserRepository(pool), postgres.NewInviteRepository(pool),
			postgres.NewSettingsRepository(pool), nil),
		Health: pool.Ping,
		Log:    log,
	})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("stopped")
	return nil
}
