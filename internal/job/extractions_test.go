package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/model"
	"video-rename/internal/pipeline"
	"video-rename/internal/sampling"
	"video-rename/internal/storage"
)

func TestExtractionWithoutModelPreservesFilesAndReusesFrames(t *testing.T) {
	m, root := setup(t)
	profile, err := (model.New(m.db)).Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	// An extraction-only batch must not resolve even a default model or prompt.
	if _, err = storage.WriteValue(t.Context(), m.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DELETE FROM model_configs") }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m.prepare = func(_ context.Context, request pipeline.PrepareRequest) (domain.FrameSet, error) {
		work := request.WorkDir

		calls.Add(1)
		return fakeFrames(work)
	}
	m.infer = func(context.Context, *config.TaskConfig, []domain.Frame) (string, error) {
		t.Error("extraction reached model")
		return "unexpected", nil
	}
	request := Request{Task: "extract", Targets: []int64{1}, Recursive: true, Key: "pre-extract"}
	id, err := m.Create(t.Context(), request, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := m.Create(t.Context(), request, "manual", 0); err != nil || same != id {
		t.Fatal("extraction request is not idempotent", same, err)
	}
	m.Execute(t.Context(), id)
	r, err := m.Get(t.Context(), id)
	if err != nil || r.Task != "extract" || r.Status != "completed" || r.Done != 1 || r.ModelConfigID != 0 || r.PromptBody != "" || m.Cache.Stats().Entries != 1 {
		t.Fatal(r, err, m.Cache.Stats())
	}
	var operations, marks int
	if err = m.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM operations),(SELECT count(*) FROM marks)").Scan(&operations, &marks)
	}); err != nil || operations != 0 || marks != 0 {
		t.Fatal("extraction modified naming state", operations, marks, err)
	}
	if _, err = os.Stat(filepath.Join(root, "a.mp4")); err != nil {
		t.Fatal(err)
	}
	var status, source, started, finished string
	var elapsed int64
	var cached bool
	if err = m.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT status,source,started,finished,duration_ms,cached FROM extraction_records WHERE run=?", id).Scan(&status, &source, &started, &finished, &elapsed, &cached)
	}); err != nil || status != "success" || source != "manual" || cached || started == "" || finished == "" || elapsed < 0 {
		t.Fatal(status, source, started, finished, elapsed, cached, err)
	}
	profile.ID = 0
	if profile, err = (model.New(m.db)).Save(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	request.Task = "rename"
	request.ModelConfigID = profile.ID
	if _, err = m.Create(t.Context(), request, "manual", 0); err == nil {
		t.Fatal("request key accepted a different task")
	}
	m.infer = func(context.Context, *config.TaskConfig, []domain.Frame) (string, error) { return "renamed", nil }
	request.Key = "rename-after-extract"
	id, err = m.Create(t.Context(), request, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r = readRun(t, m, id)
	if r.Done != 1 || calls.Load() != 1 || m.Cache.Stats().Entries != 0 {
		t.Fatal("prepared frames were not reused", r, calls.Load(), m.Cache.Stats())
	}
	if err = m.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT status,source,cached FROM extraction_records WHERE run=?", id).Scan(&status, &source, &cached)
	}); err != nil || status != "success" || source != "automatic" || !cached {
		t.Fatal(status, source, cached, err)
	}
}

func TestExtractionFailuresAreIndependentOfInferenceAndMarkedState(t *testing.T) {
	for _, task := range []string{"extract", "rename"} {
		t.Run(task, func(t *testing.T) {
			m, root := setup(t)
			for _, name := range []string{"good.mp4", "bad.mp4"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Library.Refresh(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			if task == "extract" {
				if _, err := storage.WriteValue(t.Context(), m.db, func(tx *sql.Tx) (sql.Result, error) {
					return tx.ExecContext(t.Context(), "INSERT INTO marks(identity,size,mtime_ns,source) SELECT identity,size,mtime_ns,'marked' FROM entries WHERE kind='file'")
				}); err != nil {
					t.Fatal(err)
				}
			}
			m.prepare = func(_ context.Context, request pipeline.PrepareRequest) (domain.FrameSet, error) {
				job := request.Job
				work := request.WorkDir

				if strings.HasSuffix(job.Path, "bad.mp4") {
					return domain.FrameSet{}, &domain.JobError{Code: domain.CodeExtractInsufficientFrames, Message: "计划帧全部失败", ProcessingLog: "采样时间 1s · 首次抽帧\ndecoder unavailable", StderrTail: "invalid data"}
				}
				return fakeFrames(work)
			}
			m.infer = func(context.Context, *config.TaskConfig, []domain.Frame) (string, error) {
				return "", errors.New("model failed")
			}
			id, err := m.Create(t.Context(), Request{Task: task, Targets: []int64{1}, Key: "mixed-extraction"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			m.Execute(t.Context(), id)
			var successes, failures, marks int
			if err = m.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM extraction_records WHERE status='success'),(SELECT count(*) FROM extraction_records WHERE status='failure'),(SELECT count(*) FROM marks)").Scan(&successes, &failures, &marks)
			}); err != nil || successes != 1 || failures != 1 {
				t.Fatal(successes, failures, err)
			}
			var detail string
			if err = m.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT processing_log FROM extraction_records WHERE status='failure'").Scan(&detail)
			}); err != nil || !strings.Contains(detail, "decoder unavailable") || !strings.Contains(detail, "invalid data") {
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
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	m.prepare = func(ctx context.Context, request pipeline.PrepareRequest) (domain.FrameSet, error) {

		close(started)
		<-ctx.Done()
		return domain.FrameSet{}, ctx.Err()
	}
	id, err := m.Create(t.Context(), Request{Task: "extract", Targets: []int64{1}, Key: "cancel-frames"}, "manual", 0)
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
	if err = m.Cancel(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not drain extraction")
	}
	var status, message string
	if err = m.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT status,message FROM extraction_records WHERE run=?", id).Scan(&status, &message)
	}); err != nil || status != "failure" || !strings.Contains(message, `"code":"extraction_cancelled"`) {
		t.Fatal(status, message, err)
	}
	if _, err = m.startExtraction(t.Context(), startExtractionOptions{Run: Run{}, Entry: 0, Path: "interrupted.mp4", Started: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = recoverExtractions(t.Context(), m.db); err != nil {
		t.Fatal(err)
	}
	var duration int64
	if err = m.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT status,duration_ms FROM extraction_records WHERE path='interrupted.mp4'").Scan(&status, &duration)
	}); err != nil || status != "failure" || duration < 59000 {
		t.Fatal(status, duration, err)
	}
}

func TestExtractionScansBeyondLibraryPagesAndFreezesSampling(t *testing.T) {
	m, root := setup(t)
	settings := sampling.New(m.db)
	original, err := settings.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 105 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("clip-%03d.mp4", i)), []byte("video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "child.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m.prepare = func(_ context.Context, request pipeline.PrepareRequest) (domain.FrameSet, error) {
		cfg := request.Config
		work := request.WorkDir

		if cfg.Sampling.MaxFrames != original.Sampling.MaxFrames {
			t.Errorf("run lost sampling snapshot: %d", cfg.Sampling.MaxFrames)
		}
		calls.Add(1)
		return fakeFrames(work)
	}
	id, err := m.Create(t.Context(), Request{Task: "extract", Targets: []int64{1}, Key: "extract-all-pages"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Create(t.Context(), Request{Targets: []int64{1}, Recursive: true, Key: "rename-overlap"}, "manual", 0); err == nil {
		t.Fatal("rename overlapped queued extraction")
	}
	changed := original
	changed.Images.JPEGQuality = 6
	changed.Sampling.MaxFrames = original.Sampling.MaxFrames - 1
	if err = settings.Save(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, err := m.Get(t.Context(), id)
	if err != nil || r.Done != 105 || r.Discovered != 105 || calls.Load() != 105 {
		t.Fatal("non-recursive extraction lost files across pages", r, calls.Load(), err)
	}
	if err = settings.Save(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	id, err = m.Create(t.Context(), Request{Task: "extract", Targets: []int64{1}, Recursive: true, Key: "extract-nested"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, err = m.Get(t.Context(), id)
	if err != nil || r.Done != 106 || calls.Load() != 106 {
		t.Fatal("recursive extraction did not reuse the first batch", r, calls.Load(), err)
	}
}
