package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/models"
	"video-rename/internal/prompts"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

func setup(t *testing.T) (*Manager, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "media")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(base, tool), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", base)
	db, err := storage.Open(filepath.Join(base, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	l, err := library.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(db, l, &config.Config{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	profile, e := (models.Store{DB: db}).Get(1)
	if e != nil {
		t.Fatal(e)
	}
	profile.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128}
	if _, e = (models.Store{DB: db}).Save(profile); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Cache.Close() })
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	if err := (systemsettings.Store{DB: db}).Save(settings); err != nil {
		t.Fatal(err)
	}
	return m, root
}
func TestAutomaticRenameSkipRestoreAndOverlap(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		calls.Add(1)
		return "新的标题", nil
	}
	id, err := m.Create(Request{Targets: []int64{1, 1}, Recursive: true, Key: "test-run-1"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	same, err := m.Create(Request{Targets: []int64{1, 1}, Recursive: true, Key: "test-run-1"}, "manual", 0)
	if err != nil || same != id {
		t.Fatal("request not idempotent")
	}
	m.Execute(t.Context(), id)
	r, _ := m.Get(id)
	if r.Done != 1 || r.Discovered != 1 || calls.Load() != 1 {
		t.Fatalf("bad first run %+v calls=%d", r, calls.Load())
	}
	if _, err = os.Stat(filepath.Join(root, "新的标题.mp4")); err != nil {
		t.Fatal(err)
	}
	id, err = m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "test-run-2"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ = m.Get(id)
	if r.Skipped != 1 || calls.Load() != 1 {
		t.Fatalf("duplicate %+v calls=%d", r, calls.Load())
	}
	var op int64
	if err = m.DB.QueryRow("SELECT id FROM operations WHERE source='automatic' AND status='committed'").Scan(&op); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Library.Restore(op, "test-restore"); err != nil {
		t.Fatal(err)
	}
	id, err = m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "test-run-3"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ = m.Get(id)
	if r.Done != 1 || r.Skipped != 0 || calls.Load() != 2 {
		t.Fatalf("restored file was not reprocessed %+v", r)
	}
}
func TestRunTokenCountAccumulatesAndSurvivesHistoryDeletion(t *testing.T) {
	m, root := setup(t)
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m.Analyze = func(ctx context.Context, _ *config.Config, job domain.VideoJob, _, _ string) (string, error) {
		response := `{}`
		if filepath.Base(job.Path) == "a.mp4" {
			response = `{"usage":{"total_tokens":120}}`
		}
		domain.CaptureModelDiagnostics(ctx, domain.TitleResult{RawRequest: `{"_local_context":{"text_tokens_estimate":75}}`, RawResponse: response})
		return "renamed-" + filepath.Base(job.Path), nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "token-count-run"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	run, err := m.Get(id)
	if err != nil || run.TokenCount != 195 || run.TokenRecords != 2 || run.TokenEstimated != 1 {
		t.Fatalf("run token totals: %+v, %v", run, err)
	}
	if _, err = m.DB.Exec("DELETE FROM events WHERE run=?", id); err != nil {
		t.Fatal(err)
	}
	listed, err := m.List(0)
	if err != nil || len(listed) != 1 || listed[0].TokenCount != 195 {
		t.Fatalf("persisted token totals after history deletion: %+v, %v", listed, err)
	}
}
func TestManualProtectsFileAndCancel(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var eid int64
	m.DB.QueryRow("SELECT id FROM entries WHERE name='a.mp4'").Scan(&eid)
	e, _ := m.Library.Get(eid)
	if _, err := m.Library.Rename(e.ID, e.Version, "manual.mp4", "manual-test"); err != nil {
		t.Fatal(err)
	}
	m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		t.Error("protected file analyzed")
		return "", nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "manual-protection"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ := m.Get(id)
	if r.Skipped != 1 {
		t.Fatalf("%+v", r)
	}
	id, err = m.Create(Request{Targets: []int64{1}, Key: "cancel-before-run"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ = m.Get(id)
	if r.Status != "cancelled" {
		t.Fatal(r.Status)
	}
}
func TestChangedDuringAnalysisDoesNotRename(t *testing.T) {
	m, root := setup(t)
	p := filepath.Join(root, "a.mp4")
	os.WriteFile(p, []byte("video"), 0600)
	m.Analyze = func(_ context.Context, _ *config.Config, j domain.VideoJob, _, _ string) (string, error) {
		return "changed", os.WriteFile(j.Path, []byte("different content"), 0600)
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "source-changing"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ := m.Get(id)
	if r.Failed != 1 {
		t.Fatalf("%+v", r)
	}
	if _, err = os.Stat(p); err != nil {
		t.Fatal("source moved")
	}
}
func TestCancelInFlight(t *testing.T) {
	m, root := setup(t)
	os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600)
	started := make(chan struct{})
	m.Analyze = func(ctx context.Context, _ *config.Config, _ domain.VideoJob, _, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "cancel-in-flight"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.Execute(t.Context(), id); close(done) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	if err = m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel timed out")
	}
	r, _ := m.Get(id)
	if r.Done != 0 || r.Status != "cancelled" {
		t.Fatalf("%+v", r)
	}
}

func TestPromptSnapshotAndIsolation(t *testing.T) {
	m, root := setup(t)
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := models.Store{DB: m.DB}
	libraryPrompt, err := (prompts.Store{DB: m.DB}).Save(prompts.Profile{Title: "独立提示词", Body: "批次创建时的要求"})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := store.Save(models.Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128, Workers: 1, Title: "独立提示词", PromptID: libraryPrompt.ID})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Targets: []int64{1}, ModelConfigID: prompt.ID, Key: "snapshot-run"}
	id, err := m.Create(request, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	libraryPrompt.Body = "后续修改的要求"
	if _, err = (prompts.Store{DB: m.DB}).Save(libraryPrompt); err != nil {
		t.Fatal(err)
	}
	prompt.BaseURL = "http://127.0.0.1:9999/v1"
	prompt.Model = "new-model"
	prompt.APIKey = "new-key"
	prompt.Temperature = 1.1
	prompt.Workers = 4
	if _, err = store.Save(prompt); err != nil {
		t.Fatal(err)
	}
	if again, err := m.Create(request, "manual", 0); err != nil || again != id {
		t.Fatal(again, err)
	}
	request.ModelConfigID = 1
	if _, err = m.Create(request, "manual", 0); err == nil {
		t.Fatal("different prompt accepted with same key")
	}
	request.Key = "invalid-prompt"
	request.ModelConfigID = 9999
	if _, err = m.Create(request, "manual", 0); err == nil {
		t.Fatal("missing prompt accepted")
	}
	var calls atomic.Int32
	m.Analyze = func(_ context.Context, cfg *config.Config, j domain.VideoJob, _, _ string) (string, error) {
		calls.Add(1)
		if cfg.Workers != 4 || cfg.Server.Workers != 4 {
			t.Error("latest model concurrency not applied")
		}
		if cfg.Server.Model != "test-model" || cfg.Server.BaseURL != "http://127.0.0.1:8080/v1" || cfg.Server.Temperature != 0.2 || cfg.Server.APIKey != "" {
			t.Error("model snapshot changed after queueing")
		}
		if !strings.HasPrefix(cfg.Prompt, "批次创建时的要求") || !strings.HasSuffix(cfg.Prompt, systemsettings.DefaultFixedPrompt) {
			t.Errorf("snapshot changed: %s", cfg.Prompt)
		}
		if cfg == m.Config {
			t.Error("shared config was mutated")
		}
		return "新标题" + filepath.Base(j.Path), nil
	}
	m.Execute(t.Context(), id)
	r, err := m.Get(id)
	if err != nil || r.Done != 2 || calls.Load() != 2 || !strings.HasPrefix(r.PromptBody, "批次创建时的要求") || !strings.HasSuffix(r.PromptBody, systemsettings.DefaultFixedPrompt) || r.ModelConfigTitle != "独立提示词" {
		t.Fatal(r, err, calls.Load())
	}
	request.Key = "next-prompt"
	request.ModelConfigID = prompt.ID
	next, err := m.Create(request, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.Get(next)
	if err != nil || !strings.HasPrefix(r.PromptBody, "后续修改的要求") || !strings.HasSuffix(r.PromptBody, systemsettings.DefaultFixedPrompt) {
		t.Fatal(r, err)
	}
}

func TestGlobalDefaultUsedByNewRuns(t *testing.T) {
	m, _ := setup(t)
	store := models.Store{DB: m.DB}
	libraryPrompt, err := (prompts.Store{DB: m.DB}).Save(prompts.Profile{Title: "全局默认提示词", Body: "使用新默认"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Save(models.Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128, Title: "全局默认测试", PromptID: libraryPrompt.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDefault(p.ID); err != nil {
		t.Fatal(err)
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "default-selection"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Get(id)
	if err != nil || r.ModelConfigID != p.ID || r.PromptBody != libraryPrompt.Body+"\n\n"+systemsettings.DefaultFixedPrompt {
		t.Fatal(r, err)
	}
	if err = store.SetDefault(1); err != nil {
		t.Fatal(err)
	}
	r, err = m.Get(id)
	if err != nil || r.ModelConfigID != p.ID {
		t.Fatal("queued prompt changed", r, err)
	}
}

func TestManuallyMarkedFilesSkipAndCanBeReenabled(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	result := m.Library.SetProcessed(t.Context(), []int64{1}, true)
	if result.Changed != 1 || result.Failed != 0 {
		t.Fatalf("%+v", result)
	}
	var calls int
	m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		calls++
		return "renamed", nil
	}
	for i, key := range []string{"marked-run-1", "marked-run-2"} {
		if i == 1 {
			m.Library.SetProcessed(t.Context(), []int64{1}, false)
		}
		id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: key}, "manual", 0)
		if err != nil {
			t.Fatal(err)
		}
		m.Execute(t.Context(), id)
		r, _ := m.Get(id)
		if i == 0 && (r.Skipped != 1 || calls != 0) {
			t.Fatalf("marked file processed %+v", r)
		}
		if i == 1 && (r.Done != 1 || calls != 1) {
			t.Fatalf("unmarked file skipped %+v", r)
		}
	}
}

func TestNamingFailureKeepsFileAndRecordsFailure(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "original.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		return "", &domain.JobError{Code: domain.CodeNamingFailed, Message: "命名失败：模型表示无法生成符合要求的标题，已保留原文件名", RawResponse: "命名失败"}
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "naming-failure-test"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ := m.Get(id)
	if r.Failed != 1 || r.Done != 0 || r.Status != "partial_failure" {
		t.Fatalf("%+v", r)
	}
	if _, err = os.Stat(filepath.Join(root, "original.mp4")); err != nil {
		t.Fatal(err)
	}
	var count int
	m.DB.QueryRow("SELECT count(*) FROM operations").Scan(&count)
	if count != 0 {
		t.Fatal("failure created rename operation")
	}
	m.DB.QueryRow("SELECT count(*) FROM marks").Scan(&count)
	if count != 0 {
		t.Fatal("failure marked processed")
	}
	m.DB.QueryRow("SELECT count(*) FROM events WHERE category='failure' AND action='automatic' AND raw_response='命名失败'").Scan(&count)
	if count != 1 {
		t.Fatal("missing failure history")
	}
}
func TestRunItemsRecordProcessingTiming(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "timed.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		time.Sleep(20 * time.Millisecond)
		return "计时标题", nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "timing-run-1"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	var started, finished int64
	if err = m.DB.QueryRow("SELECT started_ms,finished_ms FROM run_items WHERE run=?", id).Scan(&started, &finished); err != nil {
		t.Fatal(err)
	}
	if started <= 0 || finished < started+20 {
		t.Fatalf("processed item should record timing: started=%d finished=%d", started, finished)
	}
	// The already-processed file is skipped during the next scan: it gets a
	// completion timestamp for attribution but never a processing start.
	id, err = m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "timing-run-2"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	if err = m.DB.QueryRow("SELECT started_ms,finished_ms FROM run_items WHERE run=?", id).Scan(&started, &finished); err != nil {
		t.Fatal(err)
	}
	if started != 0 || finished <= 0 {
		t.Fatalf("scan skip should record only completion: started=%d finished=%d", started, finished)
	}
}
