// Command video-rename serves the media organizing workspace.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"video-rename/internal/app"
	"video-rename/internal/config"
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
// configuration loads, the service logs through its configured logger.
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
	path, err := o.configPath(os.LookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "video-rename: config: %v\n", err)
		return exitFailure
	}
	cfg, err := config.Load(path)
	if err == nil {
		err = cfg.ValidateService()
	}
	if err != nil {
		fmt.Fprintf(stderr, "video-rename: config: %v\n", err)
		return exitFailure
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	slog.Info("starting video-rename", "version", build.String())
	if err = app.Serve(ctx, cfg); err != nil {
		fmt.Fprintf(stderr, "video-rename: %v\n", err)
		return exitFailure
	}
	return exitOK
}
