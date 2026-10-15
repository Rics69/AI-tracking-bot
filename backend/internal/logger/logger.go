// Package logger builds the process-wide slog logger.
package logger

import (
	"log/slog"
	"os"
)

// New returns a logger writing to stdout in the given format ("text" or "json").
func New(level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
