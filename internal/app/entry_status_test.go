package app

import (
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/library"
)

func TestEntryMarksAndSnapshots(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var id int64
	a.DB.QueryRow("SELECT id FROM entries WHERE name='a.mp4'").Scan(&id)
	for _, body := range []string{`{"targets":[],"marked":true}`, `{"targets":[1]}`, `{"targets":[0],"marked":true}`} {
		w := httptest.NewRecorder()
		a.markEntries(w, jsonRequest("POST", "/api/entries/processed", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	a.markEntries(w, jsonRequest("POST", "/api/entries/processed", strings.NewReader(fmt.Sprintf(`{"targets":[%d],"marked":true}`, id))))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	a.entryStatus(w, httptest.NewRequest("GET", fmt.Sprintf("/api/entries/status?ids=1,%d,999999", id), nil))
	var entries []library.Entry
	if err = decodeItems(w.Body.Bytes(), &entries); err != nil || len(entries) != 2 || entries[1].Processed != "marked" || entries[0].Progress.Processed != 1 {
		t.Fatal(w.Body.String(), err)
	}
	for _, url := range []string{"/api/entries/status?ids=oops", "/api/entries/status?ids=-1"} {
		w = httptest.NewRecorder()
		a.entryStatus(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	for _, endpoint := range []struct{ method, url string }{{"GET", "/api/entries/status?ids=1"}, {"POST", "/api/entries/processed"}} {
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, jsonRequest(endpoint.method, endpoint.url, strings.NewReader(`{"targets":[1],"marked":true}`)))
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("unprotected endpoint", endpoint.url, w.Code)
		}
	}
}
