package app

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/library"
)

func markTestApp(t *testing.T, files int) (*App, []int64) {
	t.Helper()
	root := t.TempDir()
	for d := range 2 {
		dir := filepath.Join(root, fmt.Sprintf("dir-%d", d))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for f := range files {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("clip-%04d.mp4", f)), []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	a, err := New(t.Context(), &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdown(t, a) })
	if err := a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 2)
	for d := range 2 {
		if err := a.db.View(t.Context(), func(tx *sql.Tx) error {
			return tx.QueryRowContext(t.Context(), "SELECT id FROM entries WHERE parent=1 AND name=?", fmt.Sprintf("dir-%d", d)).Scan(&ids[d])
		}); err != nil {
			t.Fatal(err)
		}
	}
	return a, ids
}

type markRequestOptions struct {
	a        *App
	key      string
	ids      []int64
	marked   bool
	wantCode int
}

func markRequest(ctx context.Context, t *testing.T, options markRequestOptions) markStatus {
	t.Helper()
	a, key, ids, marked, wantCode := options.a, options.key, options.ids, options.marked, options.wantCode

	body, err := json.Marshal(map[string]any{"targets": ids, "marked": marked, "key": key})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.markEntries(w, jsonRequest("POST", "/api/entries/processed", strings.NewReader(string(body))).WithContext(ctx))
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
	a.Library.Busy = func(ctx context.Context, _ int64) bool {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		return false
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := markRequest(ctx, t, markRequestOptions{a: a, key: "request-survives", ids: ids, marked: true, wantCode: 202})
	if result.Status != "running" {
		t.Fatal(result)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel() // Reverse proxy/client disconnects after submission.
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "request-survives", ids: ids, marked: true, wantCode: 200})
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "request-survives", ids: ids, marked: false, wantCode: 409})
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "another-request", ids: ids, marked: false, wantCode: 409})
	close(release)
	result = awaitMark(t, a, "request-survives")
	if result.Status != "completed" || result.Changed != 280 || result.Failed != 0 || result.Interrupted {
		t.Fatal(result)
	}
	// A lost final response must return the original counts without redoing work.
	result = markRequest(t.Context(), t, markRequestOptions{a: a, key: "request-survives", ids: ids, marked: true, wantCode: 200})
	if result.Changed != 280 || result.Unchanged != 0 {
		t.Fatal(result)
	}
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "unmark-request", ids: ids, marked: false, wantCode: 202})
	result = awaitMark(t, a, "unmark-request")
	if result.Status != "completed" || result.Changed != 280 {
		t.Fatal(result)
	}
	var count int
	if err := a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM marks").Scan(&count)
	}); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestBackgroundMarksReportFailuresAndShutdown(t *testing.T) {
	t.Run("file failure", func(t *testing.T) {
		a, ids := markTestApp(t, 2)
		a.Library.Busy = func(context.Context, int64) bool { return true }
		markRequest(t.Context(), t, markRequestOptions{a: a, key: "busy-files-test", ids: ids, marked: true, wantCode: 202})
		r := awaitMark(t, a, "busy-files-test")
		if r.Status != "partial_failure" || r.Failed != 4 || len(r.Failures) != 4 {
			t.Fatal(r)
		}
	})
	t.Run("shutdown", func(t *testing.T) {
		a, ids := markTestApp(t, 2)
		entered := make(chan struct{})
		var once sync.Once
		a.Library.Busy = func(ctx context.Context, _ int64) bool {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			return false
		}
		markRequest(t.Context(), t, markRequestOptions{a: a, key: "shutdown-request", ids: ids, marked: true, wantCode: 202})
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start")
		}
		if err := a.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		r := awaitMark(t, a, "shutdown-request")
		if r.Status != "interrupted" || !r.Interrupted {
			t.Fatal(r)
		}
		markRequest(t.Context(), t, markRequestOptions{a: a, key: "after-shutdown", ids: ids, marked: true, wantCode: 503})
	})
}

func TestBackgroundMarkStatusMissingAndProtected(t *testing.T) {
	a, ids := markTestApp(t, 1)
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "short", ids: ids, marked: true, wantCode: 400})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/entries/processed/status/missing-key", nil)
	r.SetPathValue("key", "missing-key")
	a.markStatus(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"mark_result_gone"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("unprotected mark status: %d", w.Code)
	}
}

func TestBackgroundMarksContinueBeyondResultCacheCapacity(t *testing.T) {
	a, ids := markTestApp(t, 1)
	for i := range 256 {
		key := fmt.Sprintf("continuous-mark-%03d", i)
		markRequest(t.Context(), t, markRequestOptions{a: a, key: key, ids: ids, marked: i%2 == 0, wantCode: 202})
		result := awaitMark(t, a, key)
		if result.Status != "completed" || result.Changed != 2 || result.Unchanged != 0 || result.Failed != 0 {
			t.Fatalf("request %d: %+v", i, result)
		}
		a.markMu.Lock()
		cached := len(a.markJobs)
		a.markMu.Unlock()
		if cached > 128 {
			t.Fatalf("unbounded result cache: %d", cached)
		}
	}
	// Replaying a retained result must not undo a later opposite operation.
	result := markRequest(t.Context(), t, markRequestOptions{a: a, key: "continuous-mark-254", ids: ids, marked: true, wantCode: 200})
	if result.Changed != 2 || result.Unchanged != 0 {
		t.Fatal(result)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/entries/processed/status/continuous-mark-000", nil)
	r.SetPathValue("key", "continuous-mark-000")
	a.markStatus(w, r)
	if w.Code != 404 {
		t.Fatalf("retired result status=%d: %s", w.Code, w.Body.String())
	}
	var count int
	if err := a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM marks").Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("old result changed durable state: count=%d err=%v", count, err)
	}
}

func TestBackgroundMarksEvictOldestCompletedResult(t *testing.T) {
	a, ids := markTestApp(t, 1)
	now := time.Now()
	a.markJobs = make(map[string]*markJob)
	for i := range 128 {
		key := fmt.Sprintf("cached-mark-%03d", i)
		a.markJobs[key] = &markJob{
			targets: ids, marked: true, finished: now.Add(-time.Duration(128-i) * time.Minute / 4),
			status: markStatus{Key: key, Status: "completed", Changed: 2, Failures: []library.MarkFailure{}},
		}
	}
	// Lookup and validation precede eviction, including at full capacity.
	if result := markRequest(t.Context(), t, markRequestOptions{a: a, key: "cached-mark-000", ids: ids, marked: true, wantCode: 200}); result.Changed != 2 {
		t.Fatal(result)
	}
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "cached-mark-000", ids: ids, marked: false, wantCode: 409})
	if len(a.markJobs) != 128 {
		t.Fatal("lookup or conflict evicted a cached result")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.Library.Busy = func(ctx context.Context, _ int64) bool {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		return false
	}
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "new-capacity-request", ids: ids, marked: true, wantCode: 202})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	// The active worker and all more recent results survive capacity pressure.
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "blocked-capacity-request", ids: ids, marked: false, wantCode: 409})
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "new-capacity-request", ids: ids, marked: true, wantCode: 200})
	a.markMu.Lock()
	_, oldest := a.markJobs["cached-mark-000"]
	_, next := a.markJobs["cached-mark-001"]
	active := a.markJobs["new-capacity-request"]
	cached := len(a.markJobs)
	a.markMu.Unlock()
	if oldest || !next || active == nil || cached != 128 {
		t.Fatalf("wrong eviction: oldest=%v next=%v active=%v cached=%d", oldest, next, active != nil, cached)
	}
	close(release)
	if result := awaitMark(t, a, "new-capacity-request"); result.Changed != 2 || result.Status != "completed" {
		t.Fatal(result)
	}
}

func TestBackgroundMarkResultExpiresWithoutRemovingMarks(t *testing.T) {
	a, ids := markTestApp(t, 1)
	key := "expired-mark-result"
	markRequest(t.Context(), t, markRequestOptions{a: a, key: key, ids: ids, marked: true, wantCode: 202})
	awaitMark(t, a, key)
	a.markMu.Lock()
	a.markJobs[key].finished = time.Now().Add(-2 * time.Hour)
	a.markMu.Unlock()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/entries/processed/status/"+key, nil)
	r.SetPathValue("key", key)
	a.markStatus(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"mark_result_gone"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	a.markMu.Lock()
	_, cached := a.markJobs[key]
	a.markMu.Unlock()
	if cached {
		t.Fatal("expired result still retained")
	}
	var count int
	if err := a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM marks").Scan(&count)
	}); err != nil || count != 2 {
		t.Fatalf("expired result removed marks: count=%d err=%v", count, err)
	}
	markRequest(t.Context(), t, markRequestOptions{a: a, key: "after-expired-result", ids: ids, marked: false, wantCode: 202})
	if result := awaitMark(t, a, "after-expired-result"); result.Changed != 2 {
		t.Fatal(result)
	}
}
