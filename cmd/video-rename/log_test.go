package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/config"
)

func TestNewLoggerAcceptsLoadedEmptyLogSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "media_path: " + t.TempDir() + "\nauth:\n  username: admin\n  password: x\nlog:\n  level: \"\"\n  format: \"\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Source{Path: path, Explicit: true, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	logger, closeLog, err := newLogger(cfg.Log, &out)
	if err != nil {
		t.Fatalf("got %v, want a logger for explicitly empty log settings", err)
	}
	logger.Debug("hidden")
	logger.Info("shown")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "hidden") || !strings.Contains(got, `"msg":"shown"`) {
		t.Fatalf("got output %q, want JSON at info level", got)
	}
}
