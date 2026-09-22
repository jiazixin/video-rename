package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"video-rename/internal/config"
	"video-rename/internal/domain"
)

func TestBatchRestoreAndRetry(t *testing.T) {
	m, root := setup(t)
	for _, name := range []string{"a.MP4", "b.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m.Analyze = func(_ context.Context, _ *config.Config, j domain.VideoJob, _, _ string) (string, error) {
		return "new-" + filepath.Base(j.Path), nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "restore-batch"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Library.RestoreBatch(context.Background(), id); err == nil {
		t.Fatal("active batch accepted")
	}
	m.Execute(context.Background(), id)
	if err = os.WriteFile(filepath.Join(root, "a.MP4"), []byte("conflict"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := m.Library.RestoreBatch(context.Background(), id)
	if err != nil || result.Restored != 1 || result.Failed != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if err = os.Remove(filepath.Join(root, "a.MP4")); err != nil {
		t.Fatal(err)
	}
	result, err = m.Library.RestoreBatch(context.Background(), id)
	if err != nil || result.Restored != 1 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("retry %+v %v", result, err)
	}
	result, err = m.Library.RestoreBatch(context.Background(), id)
	if err != nil || result.Restored != 0 || result.Skipped != 2 {
		t.Fatalf("repeat %+v %v", result, err)
	}
	for _, name := range []string{"a.MP4", "b.mp4"} {
		if _, err = os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	var marks int
	if err = m.DB.QueryRow("SELECT count(*) FROM marks").Scan(&marks); err != nil || marks != 0 {
		t.Fatalf("marks %d %v", marks, err)
	}
}

func TestMediaDiagnosticsPersist(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "bad.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		return "", &domain.JobError{Code: domain.CodeExtractInsufficientFrames, Message: "计划 20 帧全部失败", ProcessingLog: "采样时间 1s · 首次抽帧\nffmpeg 退出码 1: Invalid data"}
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "diagnostic-run"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(context.Background(), id)
	var log string
	if err = m.DB.QueryRow("SELECT processing_log FROM events WHERE run=? AND category='failure'", id).Scan(&log); err != nil || !strings.Contains(log, "Invalid data") {
		t.Fatalf("%q %v", log, err)
	}
}

func TestBatchRestoreDoesNotUndoLaterRename(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		return "automatic", nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "later-change"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(context.Background(), id)
	var entryID int64
	if err = m.DB.QueryRow("SELECT entry FROM operations WHERE run=?", id).Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	e, err := m.Library.Get(entryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Library.Rename(e.ID, e.Version, "later.mp4", "later-manual"); err != nil {
		t.Fatal(err)
	}
	result, err := m.Library.RestoreBatch(context.Background(), id)
	if err != nil || result.Failed != 1 || result.Restored != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err = os.Stat(filepath.Join(root, "later.mp4")); err != nil {
		t.Fatal(err)
	}
}
