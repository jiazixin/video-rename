package app

import (
	"context"
	"encoding/json/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func numberingRequestTest(t *testing.T, a *App, req numberingRequest, want int) numberingStatus {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := jsonRequest("POST", "/api/numbering/jobs", strings.NewReader(string(raw)))
	r.Header.Set("Idempotency-Key", req.Key)
	a.startNumberingJob(w, r)
	if w.Code != want {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var status numberingStatus
	if want < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
	}
	return status
}
func waitNumberingTest(t *testing.T, a *App, key string) numberingStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		a.numberingMu.Lock()
		s := a.numberingJobs[key].status
		a.numberingMu.Unlock()
		if s.Status != "running" && s.Status != "cancelling" {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("numbering task did not stop")
	return numberingStatus{}
}
func TestNumberingBackgroundPreviewConfirmationAndPaging(t *testing.T) {
	a, _ := markTestApp(t, 0)
	for i := range 205 {
		name := filepath.Join(a.Library.Root, "photo-"+strings.Repeat("x", i)+".jpg")
		// Keep filenames below filesystem length limits.
		if err := os.WriteFile(name, []byte("image"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	req := numberingRequest{Key: "preview-pages", Action: "preview", Parent: 1}
	numberingRequestTest(t, a, req, 202)
	s := waitNumberingTest(t, a, req.Key)
	if s.Status != "completed" || s.Files != 205 {
		t.Fatal(s)
	}
	numberingRequestTest(t, a, req, 200)
	changed := req
	changed.Parent = 2
	numberingRequestTest(t, a, changed, 409)
	a.numberingMu.Lock()
	batches := a.numberingJobs[req.Key].result
	a.numberingMu.Unlock()
	if len(batches) != 1 {
		t.Fatal(batches)
	}
	if _, err := os.Stat(filepath.Join(a.Library.Root, "photo-.jpg")); err != nil {
		t.Fatal("preview renamed file", err)
	}
	for _, offset := range []string{"0", "100", "200"} {
		r := httptest.NewRequest("GET", "/result?offset="+offset, nil)
		r.SetPathValue("key", req.Key)
		w := httptest.NewRecorder()
		a.numberingJobResult(w, r)
		var page struct {
			Items []numberingPreviewItem `json:"items"`
			Total int                    `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		want := 100
		if offset == "200" {
			want = 5
		}
		if len(page.Items) != want || page.Total != 205 {
			t.Fatal(page)
		}
		if strings.Contains(w.Body.String(), "temp_name") || strings.Contains(w.Body.String(), "identity") {
			t.Fatal("internal journal exposed")
		}
	}
	numberingRequestTest(t, a, numberingRequest{Key: "confirmed-apply", Action: "apply", Parent: 1, Preview: req.Key}, 202)
	s = waitNumberingTest(t, a, "confirmed-apply")
	if s.Status != "completed" || s.Done != 1 || s.Files != 205 {
		t.Fatal(s)
	}
}
func TestNumberingExplicitCancelAndLease(t *testing.T) {
	for _, lease := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "lost page"}[lease], func(t *testing.T) {
			a, _ := markTestApp(t, 0)
			if err := os.WriteFile(filepath.Join(a.Library.Root, "photo.jpg"), []byte("image"), 0o600); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			// Hold the worker until the test observes cancellation; the test
			// context ends before cleanup if the test fails early.
			a.Library.Busy = func(context.Context, int64) bool {
				once.Do(func() {
					close(entered)
					select {
					case <-release:
					case <-t.Context().Done():
					}
				})
				return false
			}
			req := numberingRequest{Key: "cancel-preview", Action: "preview", Parent: 1}
			numberingRequestTest(t, a, req, 202)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker not started")
			}
			if lease {
				a.numberingMu.Lock()
				a.numberingJobs[req.Key].lease = time.Now().Add(-time.Minute)
				a.numberingMu.Unlock()
				// The lease watcher checks once a second.
				deadline := time.Now().Add(5 * time.Second)
				for {
					a.numberingMu.Lock()
					status := a.numberingJobs[req.Key].status.Status
					a.numberingMu.Unlock()
					if status == "cancelling" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("expired lease did not cancel the job")
					}
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				r := httptest.NewRequest("POST", "/cancel", nil)
				r.SetPathValue("key", req.Key)
				a.cancelNumberingJob(httptest.NewRecorder(), r)
			}
			close(release)
			s := waitNumberingTest(t, a, req.Key)
			if s.Status != "cancelled" {
				t.Fatal(s)
			}
			if _, err := os.Stat(filepath.Join(a.Library.Root, "photo.jpg")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNumberingJobsProtectedAndShutdown(t *testing.T) {
	a, _ := markTestApp(t, 0)
	for _, path := range []string{"/api/numbering/jobs/missing", "/api/numbering/jobs/missing/result"} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	if err := a.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	numberingRequestTest(t, a, numberingRequest{Key: "closed-service", Action: "preview", Parent: 1}, 503)
}

func TestNumberingCancelKeepsCompletedDirectories(t *testing.T) {
	a, ids := markTestApp(t, 0)
	for _, dir := range []string{"dir-0", "dir-1"} {
		if err := os.WriteFile(filepath.Join(a.Library.Root, dir, "photo.jpg"), []byte("image"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	numberingRequestTest(t, a, numberingRequest{Key: "partial-preview", Action: "preview", Parent: 1}, 202)
	if s := waitNumberingTest(t, a, "partial-preview"); s.Status != "completed" {
		t.Fatal(s)
	}
	a.numberingMu.Lock()
	result := a.numberingJobs["partial-preview"].result
	a.numberingMu.Unlock()
	batchIDs := []string{result[0].ID, result[1].ID}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.Library.Busy = func(ctx context.Context, id int64) bool {
		if id == ids[1] {
			once.Do(func() {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
		return false
	}
	numberingRequestTest(t, a, numberingRequest{Key: "partial-apply", Action: "apply", Parent: 1, Batches: batchIDs}, 202)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("second directory not reached")
	}
	r := httptest.NewRequest("POST", "/cancel", nil)
	r.SetPathValue("key", "partial-apply")
	a.cancelNumberingJob(httptest.NewRecorder(), r)
	close(release)
	s := waitNumberingTest(t, a, "partial-apply")
	if s.Status != "cancelled" || s.Done != 1 || s.Files != 1 {
		t.Fatal(s)
	}
	for _, name := range []string{"dir-0/1.jpg", "dir-1/photo.jpg"} {
		if _, err := os.Stat(filepath.Join(a.Library.Root, name)); err != nil {
			t.Fatal(name, err)
		}
	}
}

func TestNumberingCloseBeforeSubmit(t *testing.T) {
	a, _ := markTestApp(t, 0)
	r := httptest.NewRequest("POST", "/cancel", nil)
	r.SetPathValue("key", "close-before-submit")
	a.cancelNumberingJob(httptest.NewRecorder(), r)
	s := numberingRequestTest(t, a, numberingRequest{Key: "close-before-submit", Action: "preview", Parent: 1}, 200)
	if s.Status != "cancelled" {
		t.Fatal(s)
	}
}
