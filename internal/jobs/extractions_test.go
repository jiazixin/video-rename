package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/sampling"
)

func TestExtractionWithoutModelPreservesFilesAndReusesFrames(t *testing.T) {
	m, root := setup(t)
	profile, err := (models.Store{DB: m.DB}).Get(1)
	if err != nil {
		t.Fatal(err)
	}
	// An extraction-only batch must not resolve even a default model or prompt.
	if _, err = m.DB.Exec("DELETE FROM model_configs"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m.Prepare = func(_ context.Context, _ *config.Config, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		calls.Add(1)
		return fakeFrames(work)
	}
	m.Infer = func(context.Context, *config.Config, []domain.Frame) (string, error) {
		t.Error("extraction reached model")
		return "unexpected", nil
	}
	request := Request{Task: "extract", Targets: []int64{1}, Recursive: true, Key: "pre-extract"}
	id, err := m.Create(request, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := m.Create(request, "manual", 0); err != nil || same != id {
		t.Fatal("extraction request is not idempotent", same, err)
	}
	m.Execute(t.Context(), id)
	r, err := m.Get(t.Context(), id)
	if err != nil || r.Task != "extract" || r.Status != "completed" || r.Done != 1 || r.ModelConfigID != 0 || r.PromptBody != "" || m.Cache.Stats().Entries != 1 {
		t.Fatal(r, err, m.Cache.Stats())
	}
	var operations, marks int
	if err = m.DB.QueryRow("SELECT (SELECT count(*) FROM operations),(SELECT count(*) FROM marks)").Scan(&operations, &marks); err != nil || operations != 0 || marks != 0 {
		t.Fatal("extraction modified naming state", operations, marks, err)
	}
	if _, err = os.Stat(filepath.Join(root, "a.mp4")); err != nil {
		t.Fatal(err)
	}
	var status, source, started, finished string
	var elapsed int64
	var cached bool
	if err = m.DB.QueryRow("SELECT status,source,started,finished,duration_ms,cached FROM extraction_records WHERE run=?", id).Scan(&status, &source, &started, &finished, &elapsed, &cached); err != nil || status != "success" || source != "manual" || cached || started == "" || finished == "" || elapsed < 0 {
		t.Fatal(status, source, started, finished, elapsed, cached, err)
	}
	profile.ID = 0
	if profile, err = (models.Store{DB: m.DB}).Save(profile); err != nil {
		t.Fatal(err)
	}
	request.Task = "rename"
	request.ModelConfigID = profile.ID
	if _, err = m.Create(request, "manual", 0); err == nil {
		t.Fatal("request key accepted a different task")
	}
	m.Infer = func(context.Context, *config.Config, []domain.Frame) (string, error) { return "renamed", nil }
	request.Key = "rename-after-extract"
	id, err = m.Create(request, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ = m.Get(t.Context(), id)
	if r.Done != 1 || calls.Load() != 1 || m.Cache.Stats().Entries != 0 {
		t.Fatal("prepared frames were not reused", r, calls.Load(), m.Cache.Stats())
	}
	if err = m.DB.QueryRow("SELECT status,source,cached FROM extraction_records WHERE run=?", id).Scan(&status, &source, &cached); err != nil || status != "success" || source != "automatic" || !cached {
		t.Fatal(status, source, cached, err)
	}
}

func TestExtractionFailuresAreIndependentOfInferenceAndMarkedState(t *testing.T) {
	for _, task := range []string{"extract", "rename"} {
		t.Run(task, func(t *testing.T) {
			m, root := setup(t)
			for _, name := range []string{"good.mp4", "bad.mp4"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Library.Refresh(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			if task == "extract" {
				if _, err := m.DB.Exec("INSERT INTO marks(identity,size,mtime,source) SELECT identity,size,mtime,'marked' FROM entries WHERE kind='file'"); err != nil {
					t.Fatal(err)
				}
			}
			m.Prepare = func(_ context.Context, _ *config.Config, job domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
				if strings.HasSuffix(job.Path, "bad.mp4") {
					return domain.FrameSet{}, &domain.JobError{Code: domain.CodeExtractInsufficientFrames, Message: "计划帧全部失败", ProcessingLog: "采样时间 1s · 首次抽帧\ndecoder unavailable", StderrTail: "invalid data"}
				}
				return fakeFrames(work)
			}
			m.Infer = func(context.Context, *config.Config, []domain.Frame) (string, error) {
				return "", fmt.Errorf("model failed")
			}
			id, err := m.Create(Request{Task: task, Targets: []int64{1}, Key: "mixed-extraction"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			m.Execute(t.Context(), id)
			var successes, failures, marks int
			if err = m.DB.QueryRow("SELECT (SELECT count(*) FROM extraction_records WHERE status='success'),(SELECT count(*) FROM extraction_records WHERE status='failure'),(SELECT count(*) FROM marks)").Scan(&successes, &failures, &marks); err != nil || successes != 1 || failures != 1 {
				t.Fatal(successes, failures, err)
			}
			var detail string
			if err = m.DB.QueryRow("SELECT processing_log FROM extraction_records WHERE status='failure'").Scan(&detail); err != nil || !strings.Contains(detail, "decoder unavailable") || !strings.Contains(detail, "invalid data") {
				t.Fatal(detail, err)
			}
			if task == "extract" && marks != 2 {
				t.Fatal("processed marks changed", marks)
			}
		})
	}
}

func TestExtractionCancellationAndInterruptedRecords(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	m.Prepare = func(ctx context.Context, _ *config.Config, _ domain.VideoJob, _, _, _ string) (domain.FrameSet, error) {
		close(started)
		<-ctx.Done()
		return domain.FrameSet{}, ctx.Err()
	}
	id, err := m.Create(Request{Task: "extract", Targets: []int64{1}, Key: "cancel-frames"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); m.Execute(t.Context(), id) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("extraction did not start")
	}
	if err = m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not drain extraction")
	}
	var status, message string
	if err = m.DB.QueryRow("SELECT status,message FROM extraction_records WHERE run=?", id).Scan(&status, &message); err != nil || status != "failure" || message != "抽帧已取消" {
		t.Fatal(status, message, err)
	}
	if _, err = m.startExtraction(Run{}, 0, "interrupted.mp4", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = recoverExtractions(m.DB); err != nil {
		t.Fatal(err)
	}
	var duration int64
	if err = m.DB.QueryRow("SELECT status,duration_ms FROM extraction_records WHERE path='interrupted.mp4'").Scan(&status, &duration); err != nil || status != "failure" || duration < 59000 {
		t.Fatal(status, duration, err)
	}
}

func TestExtractionScansBeyondLibraryPagesAndFreezesSampling(t *testing.T) {
	m, root := setup(t)
	settings := sampling.Store{DB: m.DB}
	original, err := settings.Get()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 105 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("clip-%03d.mp4", i)), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "child.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m.Prepare = func(_ context.Context, cfg *config.Config, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		if cfg.Sampling.MaxFrames != original.Sampling.MaxFrames {
			t.Errorf("run lost sampling snapshot: %d", cfg.Sampling.MaxFrames)
		}
		calls.Add(1)
		return fakeFrames(work)
	}
	id, err := m.Create(Request{Task: "extract", Targets: []int64{1}, Key: "extract-all-pages"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "rename-overlap"}, "manual", 0); err == nil {
		t.Fatal("rename overlapped queued extraction")
	}
	changed := original
	changed.Images.JPEGQuality = 6
	changed.Sampling.MaxFrames = original.Sampling.MaxFrames - 1
	if err = settings.Save(changed); err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, err := m.Get(t.Context(), id)
	if err != nil || r.Done != 105 || r.Discovered != 105 || calls.Load() != 105 {
		t.Fatal("non-recursive extraction lost files across pages", r, calls.Load(), err)
	}
	if err = settings.Save(original); err != nil {
		t.Fatal(err)
	}
	id, err = m.Create(Request{Task: "extract", Targets: []int64{1}, Recursive: true, Key: "extract-nested"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, err = m.Get(t.Context(), id)
	if err != nil || r.Done != 106 || calls.Load() != 106 {
		t.Fatal("recursive extraction did not reuse the first batch", r, calls.Load(), err)
	}
}
