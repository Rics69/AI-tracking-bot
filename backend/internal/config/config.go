// Package config loads service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
	_ "time/tzdata" // embed the tz database: minimal images have no /usr/share/zoneinfo

	"github.com/caarlos0/env/v11"
)

// Common is the configuration shared by every binary.
type Common struct {
	DatabaseURL string     `env:"DATABASE_URL,required"`
	Timezone    string     `env:"APP_TIMEZONE" envDefault:"Europe/Moscow"`
	LogLevel    slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat   string     `env:"LOG_FORMAT" envDefault:"text"`
	// MetricsAddr serves /metrics for Prometheus; keep it off the public network.
	MetricsAddr string `env:"METRICS_ADDR" envDefault:":9091"`

	// Location is parsed from Timezone by Load.
	Location *time.Location `env:"-"`
}

// API is the configuration of the REST API server.
type API struct {
	Common
	HTTPAddr        string        `env:"HTTP_ADDR" envDefault:":8080"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

// LLM selects and configures the model provider.
type LLM struct {
	Provider string   `env:"LLM_PROVIDER" envDefault:"gigachat"`
	GigaChat GigaChat `envPrefix:"GIGACHAT_"`
}

// GigaChat configures the GigaChat provider. Empty URLs mean the defaults.
type GigaChat struct {
	AuthKey            string        `env:"AUTH_KEY"`
	Scope              string        `env:"SCOPE" envDefault:"GIGACHAT_API_PERS"`
	Model              string        `env:"MODEL" envDefault:"GigaChat-2-Pro"`
	BaseURL            string        `env:"BASE_URL"`
	AuthURL            string        `env:"AUTH_URL"`
	Temperature        *float64      `env:"TEMPERATURE"`
	Timeout            time.Duration `env:"TIMEOUT" envDefault:"30s"`
	MaxRetries         int           `env:"MAX_RETRIES" envDefault:"3"`
	MaxConcurrent      int           `env:"MAX_CONCURRENT" envDefault:"1"`
	InsecureSkipVerify bool          `env:"INSECURE_SKIP_VERIFY"`
}

// Agent tunes the conversational agent.
type Agent struct {
	HistoryTurns int           `env:"AGENT_HISTORY_TURNS" envDefault:"6"`
	HistoryTTL   time.Duration `env:"AGENT_HISTORY_TTL" envDefault:"30m"`
	MaxSteps     int           `env:"AGENT_MAX_STEPS" envDefault:"6"`
}

// Bot is the configuration of the Telegram bot.
type Bot struct {
	Common
	LLM
	Agent
	TelegramToken string `env:"TELEGRAM_BOT_TOKEN,required"`
	// AllowedUserIDs are the family heads, added on every start. Others join by
	// invite link. Empty: an open family where anyone who writes joins.
	AllowedUserIDs []int64       `env:"ALLOWED_TELEGRAM_IDS" envSeparator:","`
	ReplyTimeout   time.Duration `env:"REPLY_TIMEOUT" envDefault:"90s"`
}

// Chat is the configuration of the local agent REPL (cmd/chat).
type Chat struct {
	Common
	LLM
	Agent
}

// Eval is the configuration of the agent eval runner (cmd/eval): an LLM, no database.
type Eval struct {
	LLM
	Agent
	Timezone string     `env:"APP_TIMEZONE" envDefault:"Europe/Moscow"`
	LogLevel slog.Level `env:"LOG_LEVEL" envDefault:"warn"`

	Location *time.Location `env:"-"`
}

// LoadEval reads the eval runner configuration from the environment.
func LoadEval() (Eval, error) {
	var cfg Eval
	if err := env.Parse(&cfg); err != nil {
		return Eval{}, fmt.Errorf("parse env: %w", err)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return Eval{}, fmt.Errorf("APP_TIMEZONE %q: %w", cfg.Timezone, err)
	}
	cfg.Location = loc
	if err := cfg.validate(); err != nil {
		return Eval{}, err
	}
	return cfg, nil
}

// LoadAPI reads the API server configuration from the environment.
func LoadAPI() (API, error) {
	var cfg API
	if err := load(&cfg, &cfg.Common); err != nil {
		return API{}, err
	}
	return cfg, nil
}

// LoadBot reads the Telegram bot configuration from the environment.
func LoadBot() (Bot, error) {
	var cfg Bot
	if err := load(&cfg, &cfg.Common); err != nil {
		return Bot{}, err
	}
	if err := cfg.validate(); err != nil {
		return Bot{}, err
	}
	return cfg, nil
}

// LoadChat reads the REPL configuration from the environment.
func LoadChat() (Chat, error) {
	var cfg Chat
	if err := load(&cfg, &cfg.Common); err != nil {
		return Chat{}, err
	}
	if err := cfg.validate(); err != nil {
		return Chat{}, err
	}
	return cfg, nil
}

func (l LLM) validate() error {
	switch l.Provider {
	case "gigachat":
		if l.GigaChat.AuthKey == "" {
			return errors.New("GIGACHAT_AUTH_KEY is required for LLM_PROVIDER=gigachat")
		}
		return nil
	default:
		return fmt.Errorf("unsupported LLM_PROVIDER %q (supported: gigachat)", l.Provider)
	}
}

func load(cfg any, common *Common) error {
	if err := env.Parse(cfg); err != nil {
		return fmt.Errorf("parse env: %w", err)
	}
	loc, err := time.LoadLocation(common.Timezone)
	if err != nil {
		return fmt.Errorf("APP_TIMEZONE %q: %w", common.Timezone, err)
	}
	common.Location = loc
	if common.LogFormat != "text" && common.LogFormat != "json" {
		return fmt.Errorf("LOG_FORMAT must be text or json, got %q", common.LogFormat)
	}
	return nil
}
