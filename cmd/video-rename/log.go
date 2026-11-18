package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"video-rename/internal/config"
)

// newLogger writes records to the configured destination with the configured level and format.
// config.Load guarantees both are known and replaces empty values by defaults.
func newLogger(c config.LogConfig, stdout io.Writer) (*slog.Logger, func() error, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		return nil, nil, fmt.Errorf("parse log level: %w", err)
	}
	closeLog := func() error { return nil } // stdout is owned by the caller.
	output := stdout
	if c.File != "" {
		if err := os.MkdirAll(filepath.Dir(c.File), 0o700); err != nil {
			return nil, nil, fmt.Errorf("create log directory: %w", err)
		}
		file, err := os.OpenFile(c.File, os.O_APPEND|os.O_WRONLY|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
			return nil, nil, errors.Join(err, file.Close())
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
			return nil, nil, errors.Join(errors.New("log destination must be a regular file with a single link"), file.Close())
		}
		output, closeLog = file, file.Close
	}
	options := &slog.HandlerOptions{Level: level}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(output, options)), closeLog, nil
	}
	return slog.New(slog.NewJSONHandler(output, options)), closeLog, nil
}
