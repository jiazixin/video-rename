package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"video-rename/internal/config"
	"video-rename/internal/jobs"
	"video-rename/internal/library"
	"video-rename/internal/models"
	"video-rename/internal/prompts"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

func TestRulePreview(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct{ expr, zone, want string }{{"15 * * * *", "Asia/Shanghai", "2026-09-20T08:15:00+08:00"}, {"30 2 * * *", "Asia/Shanghai", "2026-09-21T02:30:00+08:00"}, {"0 3 * * 1", "Asia/Shanghai", "2026-09-21T03:00:00+08:00"}} {
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
	for _, s := range v {
		wall := s[:16]
		if seen[wall] {
			t.Fatal("duplicate DST wall time", v)
		}
		seen[wall] = true
	}
}

func TestPersistentTriggerDeduplicationAndOverlap(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	os.Mkdir(root, 0700)
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		os.WriteFile(filepath.Join(base, name), []byte("#!/bin/sh\nexit 0\n"), 0700)
	}
	t.Setenv("PATH", base)
	db, err := storage.Open(filepath.Join(base, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := library.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := jobs.New(db, l, &config.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cache.Close()
	s := &Scheduler{DB: db, Jobs: m}
	libraryPrompt, err := (prompts.Store{DB: db}).Save(prompts.Profile{Title: "计划提示词", Body: "计划专属要求"})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := (models.Store{DB: db}).Save(models.Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128, Title: "计划配置", PromptID: libraryPrompt.ID})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Plan{ModelConfigID: prompt.ID, Name: "test", Targets: []int64{1}, Recursive: true, Kind: "cron", Expression: "* * * * *", Timezone: "Asia/Shanghai", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, p.Next)
	if err = s.Tick(at); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(at); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM runs").Scan(&n)
	var savedModelConfigID int64
	var savedBody string
	if err = db.QueryRow("SELECT model_config_id,prompt_body FROM runs LIMIT 1").Scan(&savedModelConfigID, &savedBody); err != nil || savedModelConfigID != prompt.ID || savedBody != libraryPrompt.Body+"\n\n"+systemsettings.DefaultFixedPrompt {
		t.Fatal(savedModelConfigID, savedBody, err)
	}
	if n != 1 {
		t.Fatalf("duplicate triggers %d", n)
	}
	if err = s.Tick(at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT count(*) FROM runs").Scan(&n)
	if n != 1 {
		t.Fatalf("overlapping trigger %d", n)
	}
	p, _ = s.Get(p.ID)
	if p.Last != "前次任务未结束，本次跳过" {
		t.Fatal(p.Last)
	}
	if err = s.ResetNext(at.Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT count(*) FROM runs").Scan(&n)
	if n != 1 {
		t.Fatal("restart caught up missed runs")
	}
	p.Enabled = false
	if _, err = s.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(p); err == nil {
		t.Fatal("stale edit accepted")
	}
	if err = l.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
}
