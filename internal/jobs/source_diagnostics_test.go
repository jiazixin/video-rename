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
)

func TestDirectoryScanFailurePersistsDiagnostics(t *testing.T) {
	m, root := setup(t)
	path := filepath.Join(root, "folder")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var entry int64
	if err := m.DB.QueryRow("SELECT id FROM entries WHERE name='folder'").Scan(&entry); err != nil {
		t.Fatal(err)
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{entry}, Recursive: true, Key: "scan-diagnostics"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DB.Exec("UPDATE entries SET identity='999:888' WHERE id=?", entry); err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	var message, detail, oldPath string
	if err := m.DB.QueryRow("SELECT message,processing_log,old_path FROM events WHERE run=? AND action='scan'", id).Scan(&message, &detail, &oldPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, `"stage":"path_identity"`) || !strings.Contains(oldPath, "folder") {
		t.Fatalf("message=%s path=%s", message, oldPath)
	}
	for _, want := range []string{path, "999 ->", "888 ->", "kind: directory"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing %q: %s", want, detail)
		}
	}
}

func TestRunFailurePreservesWrappedDiagnostics(t *testing.T) {
	m, _ := setup(t)
	cause := &domain.JobError{Code: domain.CodeSourceChanged, Message: "changed", ProcessingLog: "source evidence", RawRequest: "request", RawResponse: "response", StderrTail: "stderr"}
	if err := m.recordFailure(t.Context(), "run", 0, 0, "", fmt.Errorf("scan failed: %w", cause)); err != nil {
		t.Fatal(err)
	}
	var message, detail, request, response string
	if err := m.DB.QueryRow("SELECT message,processing_log,raw_request,raw_response FROM events WHERE action='run'").Scan(&message, &detail, &request, &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "scan failed:") || detail != "source evidencestderr" || request != "request" || response != "response" {
		t.Fatalf("lost diagnostic: %q %q %q %q", message, detail, request, response)
	}
}

func TestSourceChangeWhileWaitingPersistsDiagnostics(t *testing.T) {
	m, root := setup(t)
	path := filepath.Join(root, "a.mp4")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Reserve every slot so the real processing path must wait after extraction.
	release1, _, err := m.modelSlot(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	release2, _, err := m.modelSlot(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release2()
		}
	}()
	m.Prepare = func(_ context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		return fakeFrames(work)
	}
	var calls atomic.Int32
	m.Infer = func(context.Context, *config.TaskConfig, []domain.Frame) (string, error) {
		calls.Add(1)
		return "unexpected", nil
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Recursive: true, Key: "source-diagnostics"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); m.Execute(ctx, id) }()
	defer func() { cancel(); <-done }()
	for {
		var count int
		if err := m.DB.QueryRow("SELECT count(*) FROM extraction_records WHERE status='success'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("extraction did not complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	release2()
	released = true
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("run did not finish")
	}
	var message, detail string
	if err := m.DB.QueryRow("SELECT message,processing_log FROM events WHERE run=? AND category='failure'", id).Scan(&message, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, `"stage":"before_request"`) || calls.Load() != 0 {
		t.Fatalf("message=%s model calls=%d", message, calls.Load())
	}
	for _, want := range []string{path, "changed fields: mtime", "device: ", "inode: ", "size (bytes): ", "waited for model: ", "frames prepared: 1", "The model was not called"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing %q in persisted diagnostic: %s", want, detail)
		}
	}
}
