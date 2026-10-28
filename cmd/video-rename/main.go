// Command video-rename serves the media organizing workspace.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"video-rename/internal/app"
	"video-rename/internal/config"
	"video-rename/internal/timefmt"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run reports usage and startup errors as plain text on stderr; once the
// logger is ready, failures are logged.
func run(args []string, stdout, stderr io.Writer) int {
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "video-rename: %v\nRun 'video-rename --help' for usage.\n", err)
		return exitUsage
	}
	build := currentBuild()
	switch {
	case o.help:
		fmt.Fprint(stdout, usage)
		return exitOK
	case o.version:
		fmt.Fprintln(stdout, "video-rename", build)
		return exitOK
	}
	path, explicit, err := o.configPath(os.LookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "video-rename: config: %v\n", err)
		return exitFailure
	}
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "video-rename: config: %v\n", err)
		return exitFailure
	}
	cfg, err := config.Load(config.Source{Path: path, Explicit: explicit, WorkDir: workDir, LookupEnv: os.LookupEnv})
	if err != nil {
		fmt.Fprintf(stderr, "video-rename: config: %v\n", err)
		return exitFailure
	}
	logger, closeLog, err := newLogger(cfg.Log, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "video-rename: log: %v\n", err)
		return exitFailure
	}
	defer func() {
		if err := closeLog(); err != nil {
			fmt.Fprintf(stderr, "video-rename: log: %v\n", err)
		}
	}()
	serverBuild, err := applicationBuild(build)
	if err != nil {
		logger.Error("service_failed", "error", err)
		return exitFailure
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal terminates the process
	logger.Info("service_starting", "version", build.String())
	if err := app.Serve(ctx, app.ServeOptions{Config: cfg, Logger: logger, Build: serverBuild}); err != nil {
		logger.Error("service_failed", "error", err)
		return exitFailure
	}
	logger.Info("service_stopped")
	return exitOK
}

func applicationBuild(build buildInfo) (app.Build, error) {
	result := app.Build{Version: build.Version, Revision: build.Revision, Modified: build.Modified}
	if build.Time != "" {
		at, err := time.Parse(time.RFC3339, build.Time)
		if err != nil {
			return app.Build{}, fmt.Errorf("parse build timestamp: %w", err)
		}
		result.Time = timefmt.Time(at)
	}
	return result, nil
}
