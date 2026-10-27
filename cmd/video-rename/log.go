package main

import (
	"io"
	"log/slog"

	"video-rename/internal/config"
)

// newLogger writes records to stdout with the configured level and format;
// the validated configuration guarantees both are known.
func newLogger(c config.LogConfig, stdout io.Writer) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(c.Level))
	options := &slog.HandlerOptions{Level: level}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(stdout, options))
	}
	return slog.New(slog.NewJSONHandler(stdout, options))
}
