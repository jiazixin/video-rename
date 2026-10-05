package app

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/config"
)

func markTestApp(t *testing.T, files int) (*App, []int64) {
	t.Helper()
	root := t.TempDir()
	for d := range 2 {
		dir := filepath.Join(root, fmt.Sprintf("dir-%d", d))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for f := range files {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("clip-%04d.mp4", f)), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if err := a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 2)
	for d := range 2 {
		if err := a.DB.QueryRow("SELECT id FROM entries WHERE parent=1 AND name=?", fmt.Sprintf("dir-%d", d)).Scan(&ids[d]); err != nil {
			t.Fatal(err)
		}
	}
	return a, ids
}

func markRequest(t *testing.T, a *App, ctx context.Context, key string, ids []int64, marked bool, wantCode int) markStatus {
	t.Helper()
	body, err := json.Marshal(map[string]any{"targets": ids, "marked": marked, "key": key})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.markEntries(w, httptest.NewRequest("POST", "/api/entries/processed", strings.NewReader(string(body))).WithContext(ctx))
	if w.Code != wantCode {
		t.Fatalf("status=%d, want %d: %s", w.Code, wantCode, w.Body.String())
	}
	var result markStatus
	if w.Code < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func awaitMark(t *testing.T, a *App, key string) markStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/entries/processed/status/"+key, nil)
		r.SetPathValue("key", key)
		a.markStatus(w, r)
		var result markStatus
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if result.Status != "running" {
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("mark did not finish")
	return markStatus{}
}

func TestBackgroundMarksSurviveRequestCancellation(t *testing.T) {
	a, ids := markTestApp(t, 140) // Crosses multiple pages in both directories.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.Library.Busy = func(int64) bool {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-a.ctx.Done():
			}
		})
		return false
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := markRequest(t, a, ctx, "request-survives", ids, true, 202)
	if result.Status != "running" {
		t.Fatal(result)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel() // Reverse proxy/client disconnects after submission.
	markRequest(t, a, t.Context(), "request-survives", ids, true, 200)
	markRequest(t, a, t.Context(), "request-survives", ids, false, 409)
	markRequest(t, a, t.Context(), "another-request", ids, false, 409)
	close(release)
	result = awaitMark(t, a, "request-survives")
	if result.Status != "completed" || result.Changed != 280 || result.Failed != 0 || result.Interrupted {
		t.Fatal(result)
	}
	// A lost final response must return the original counts without redoing work.
	result = markRequest(t, a, t.Context(), "request-survives", ids, true, 200)
	if result.Changed != 280 || result.Unchanged != 0 {
		t.Fatal(result)
	}
	markRequest(t, a, t.Context(), "unmark-request", ids, false, 202)
	result = awaitMark(t, a, "unmark-request")
	if result.Status != "completed" || result.Changed != 280 {
		t.Fatal(result)
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM marks").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestBackgroundMarksReportFailuresAndShutdown(t *testing.T) {
	t.Run("file failure", func(t *testing.T) {
		a, ids := markTestApp(t, 2)
		a.Library.Busy = func(int64) bool { return true }
		markRequest(t, a, t.Context(), "busy-files-test", ids, true, 202)
		r := awaitMark(t, a, "busy-files-test")
		if r.Status != "partial_failure" || r.Failed != 4 || len(r.Failures) != 4 {
			t.Fatal(r)
		}
	})
	t.Run("shutdown", func(t *testing.T) {
		a, ids := markTestApp(t, 2)
		entered := make(chan struct{})
		var once sync.Once
		a.Library.Busy = func(int64) bool {
			once.Do(func() { close(entered) })
			<-a.ctx.Done()
			return false
		}
		markRequest(t, a, t.Context(), "shutdown-request", ids, true, 202)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start")
		}
		a.cancel()
		r := awaitMark(t, a, "shutdown-request")
		if r.Status != "interrupted" || !r.Interrupted {
			t.Fatal(r)
		}
		markRequest(t, a, t.Context(), "after-shutdown", ids, true, 503)
	})
}

func TestBackgroundMarkStatusMissingAndProtected(t *testing.T) {
	a, ids := markTestApp(t, 1)
	markRequest(t, a, t.Context(), "short", ids, true, 400)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/entries/processed/status/missing-key", nil)
	r.SetPathValue("key", "missing-key")
	a.markStatus(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "已完成的标记仍保留") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unprotected mark status: %d", w.Code)
	}
}
