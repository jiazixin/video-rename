package main

import (
	"fmt"
	"io"
	"log/slog"

	"video-rename/internal/config"
)

// newLogger writes records to stdout with the configured level and format;
// the validated configuration guarantees both are known.
func newLogger(c config.LogConfig, stdout io.Writer) (*slog.Logger, func() error, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		return nil, nil, fmt.Errorf("parse log level: %w", err)
	}
	closeLog := func() error { return nil } // stdout is owned by the caller.
	options := &slog.HandlerOptions{Level: level}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(stdout, options)), closeLog, nil
	}
	return slog.New(slog.NewJSONHandler(stdout, options)), closeLog, nil
}
