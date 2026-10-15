// Package config loads service configuration from environment variables.
package config

import (
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

	// Location is parsed from Timezone by Load.
	Location *time.Location `env:"-"`
}

// API is the configuration of the REST API server.
type API struct {
	Common
	HTTPAddr        string        `env:"HTTP_ADDR" envDefault:":8080"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

// LoadAPI reads the API server configuration from the environment.
func LoadAPI() (API, error) {
	var cfg API
	if err := load(&cfg, &cfg.Common); err != nil {
		return API{}, err
	}
	return cfg, nil
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
