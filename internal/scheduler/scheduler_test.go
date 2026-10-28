package scheduler

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/job"
	"video-rename/internal/library"
	"video-rename/internal/model"
	promptstore "video-rename/internal/prompt"
	"video-rename/internal/storage"
	"video-rename/internal/systemsetting"
)

func TestRunReportsUnrecordableFailure(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(t.Context(), filepath.Join(base, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := library.New(t.Context(), db, root, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := New(db, &job.Manager{Library: l})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := s.Run(ctx); err == nil || !strings.Contains(err.Error(), "record scheduler failure") {
		t.Fatalf("got %v, want an observable scheduler recording failure", err)
	}
}

func TestRulePreview(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct{ expr, zone, want string }{{"15 * * * *", "Asia/Shanghai", "2026-09-20T00:15:00.000Z"}, {"30 2 * * *", "Asia/Shanghai", "2026-09-20T18:30:00.000Z"}, {"0 3 * * 1", "Asia/Shanghai", "2026-09-20T19:00:00.000Z"}} {
		v, e := Preview(c.expr, c.zone, now)
		if e != nil || len(v) != 5 || v[0] != c.want {
			t.Fatalf("%+v: %v %v", c, v, e)
		}
	}
	for _, expr := range []string{"* * * * * *", "60 * * * *", "@hourly", "0 0 31 2 *"} {
		if _, err := Preview(expr, "Asia/Shanghai", now); err == nil {
			t.Fatal(expr)
		}
	}
	if _, err := Rule("* * * * *", "invalid/zone"); err == nil {
		t.Fatal("bad zone accepted")
	}
}
func TestDSTPreviewNoDuplicateWallTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	v, err := Preview("30 1 * * *", "America/New_York", now)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range v {
		instant, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		wall := instant.In(location).Format("2006-01-02 15:04")
		if seen[wall] {
			t.Fatal("duplicate DST wall time", v)
		}
		seen[wall] = true
	}
}

func TestPersistentTriggerDeduplicationAndOverlap(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Error(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Error(err)
		}
	}
	t.Setenv("PATH", base)
	db, err := storage.Open(t.Context(), filepath.Join(base, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	l, err := library.New(t.Context(), db, root, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m, err := job.New(t.Context(), job.Options{Store: db, Library: l, Config: &config.Config{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(m.Cache.Close)
	s := &Scheduler{db: db, Jobs: m}
	libraryPrompt, err := (promptstore.New(db)).Save(t.Context(), promptstore.Profile{Title: "计划提示词", Body: "计划专属要求"})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := (model.New(db)).Save(t.Context(), model.Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", TimeoutMS: 2000, Temperature: 0.2, MaxTokens: 128, Title: "计划配置", PromptID: libraryPrompt.ID})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(t.Context(), Plan{ModelConfigID: prompt.ID, Name: "  " + strings.Repeat("计", 80) + "  ", Targets: []int64{1}, Recursive: true, Kind: "cron", Expression: "* * * * *", Timezone: "Asia/Shanghai", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != strings.Repeat("计", 80) {
		t.Fatalf("plan name was not trimmed: %q", p.Name)
	}
	tooLong := p
	tooLong.Name += "划"
	if _, err := s.Save(t.Context(), tooLong); err == nil {
		t.Fatal("accepted plan name longer than 80 characters")
	}
	at, err := time.Parse(time.RFC3339, p.Next)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM runs").Scan(&n) }); err != nil {
		t.Error(err)
	}
	var savedModelConfigID int64
	var savedBody string
	if err = db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT model_config_id,prompt_body FROM runs LIMIT 1").Scan(&savedModelConfigID, &savedBody)
	}); err != nil || savedModelConfigID != prompt.ID || savedBody != libraryPrompt.Body+"\n\n"+systemsetting.DefaultFixedPrompt {
		t.Fatal(savedModelConfigID, savedBody, err)
	}
	if n != 1 {
		t.Fatalf("duplicate triggers %d", n)
	}
	if err = s.Tick(t.Context(), at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM runs").Scan(&n) }); err != nil {
		t.Error(err)
	}
	if n != 1 {
		t.Fatalf("overlapping trigger %d", n)
	}
	p, err = s.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Last.Code != "schedule_skipped_active" {
		t.Fatal(p.Last)
	}
	if err = s.ResetNext(t.Context(), at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM runs").Scan(&n) }); err != nil {
		t.Error(err)
	}
	if n != 1 {
		t.Fatal("restart caught up missed runs")
	}
	p.Enabled = false
	if _, err = s.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(t.Context(), p); err == nil {
		t.Fatal("stale edit accepted")
	}
	// Switching a persisted plan to extraction must bypass model validation,
	// survive reload, and pass the task type through the automatic trigger.
	p, err = s.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.Task, p.ModelConfigID, p.Enabled = "extract", 999999, true
	p, err = s.Save(t.Context(), p)
	if err != nil || p.Task != "extract" || p.ModelConfigID != 0 {
		t.Fatalf("extract plan: %+v %v", p, err)
	}
	if _, err = storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE runs SET status='cancelled'")
	}); err != nil {
		t.Fatal(err)
	}
	at, err = time.Parse(time.RFC3339, p.Next)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The prior wall-clock tick can already be deduplicated; trigger the next one.
	p, err = s.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	at, err = time.Parse(time.RFC3339, p.Next)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	var task string
	if err = db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT task,model_config_id,prompt_body FROM runs ORDER BY id DESC LIMIT 1").Scan(&task, &savedModelConfigID, &savedBody)
	}); err != nil || task != "extract" || savedModelConfigID != 0 || savedBody != "" {
		t.Fatal(task, savedModelConfigID, savedBody, err)
	}
	if err = l.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
}
