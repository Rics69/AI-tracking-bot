// Command chat is a local REPL for the expense agent: it talks to the real LLM
// and database as a given user, without Telegram. Handy for trying prompts.
//
//	DATABASE_URL=... GIGACHAT_AUTH_KEY=... go run ./cmd/chat -user 1 -name Тест
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/agent"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/config"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm/provider"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/logger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/storage/postgres"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func main() {
	userID := flag.Int64("user", 1, "Telegram user ID to act as")
	name := flag.String("name", "Тест", "user's first name")
	flag.Parse()

	if err := run(*userID, *name); err != nil {
		fmt.Fprintln(os.Stderr, "chat:", err)
		os.Exit(1)
	}
}

func run(userID int64, name string) error {
	cfg, err := config.LoadChat()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel, cfg.LogFormat)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	if _, err := postgres.NewUserRepository(pool).Admit(ctx, user.User{TelegramID: userID, FirstName: name}); err != nil {
		return err
	}

	client, err := provider.New(cfg.LLM, log)
	if err != nil {
		return err
	}
	expenses := ledger.NewService(postgres.NewOperationRepository(pool), cfg.Location)
	assistant := agent.New(client, expenses, expenses,
		agent.NewHistory(cfg.HistoryTurns, cfg.HistoryTTL),
		agent.Config{Location: cfg.Location, MaxSteps: cfg.MaxSteps}, log)

	fmt.Printf("Агент готов (пользователь %d, %s). Пустая строка или Ctrl+D — выход.\n", userID, name)
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !in.Scan() || strings.TrimSpace(in.Text()) == "" {
			return nil
		}
		reply, err := assistant.Reply(ctx, agent.Caller{UserID: userID, Name: name}, in.Text())
		if err != nil {
			fmt.Println("ошибка:", err)
			continue
		}
		fmt.Println(reply)
	}
}
