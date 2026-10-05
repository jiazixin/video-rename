package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"video-rename/internal/app"
	"video-rename/internal/config"
)

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if o.Help {
		fmt.Print(usageText)
		return
	}
	if o.Version {
		fmt.Println("video-rename", versionString())
		return
	}
	cfg, err := config.Load(o.ConfigPath)
	if err == nil {
		err = cfg.ValidateService()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	slog.Info("starting video-rename", "version", versionString())
	if err = app.Serve(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
