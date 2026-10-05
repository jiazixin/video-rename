package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/libraryscan"
	"video-rename/internal/systemsettings"
)

func scanAPI(t *testing.T) (*App, string, func(string, string, string, bool) *httptest.ResponseRecorder) {
	t.Helper()
	root := t.TempDir()
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	var cookies []*http.Cookie
	var csrf string
	request := func(method, path, body string, token bool) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if token {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/library/scan", "", false); w.Code != http.StatusUnauthorized {
		t.Fatalf("scan status lacks auth: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/directories/1/state", "", false); w.Code != http.StatusUnauthorized {
		t.Fatalf("directory state lacks auth: %d %s", w.Code, w.Body.String())
	}
	w := request("POST", "/api/auth/login", `{"username":"admin","password":"test-password-123"}`, false)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies = w.Result().Cookies()
	var session map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	csrf = session["csrf"]
	return a, root, request
}

func TestLibraryScanAPISettingsAndDiscovery(t *testing.T) {
	a, root, request := scanAPI(t)
	var response struct {
		Settings systemsettings.ScanSettings `json:"settings"`
		Status   libraryscan.Status          `json:"status"`
	}
	w := request("GET", "/api/library/scan", "", false)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || !response.Settings.Enabled || response.Settings.IntervalMinutes != 60 {
		t.Fatalf("scan defaults: %d %s", w.Code, w.Body.String())
	}
	for _, target := range []struct{ method, path, body string }{
		{"PUT", "/api/library/scan/settings", `{"enabled":true,"interval_minutes":60,"targets":[1]}`},
		{"POST", "/api/library/scan", `{}`},
		{"POST", "/api/library/scan/cancel", `{}`},
	} {
		if w := request(target.method, target.path, target.body, false); w.Code != http.StatusForbidden {
			t.Fatalf("write lacks CSRF: %s %d", target.path, w.Code)
		}
	}
	for _, body := range []string{
		`{}`, `{"enabled":true,"interval_minutes":4,"targets":[1]}`,
		`{"enabled":true,"interval_minutes":60,"targets":[]}`,
		`{"enabled":true,"interval_minutes":60,"targets":[99999]}`,
		`{"enabled":true,"interval_minutes":60,"targets":[1],"extra":true}`,
	} {
		if w := request("PUT", "/api/library/scan/settings", body, true); w.Code < 400 {
			t.Fatalf("accepted invalid settings: %s", body)
		}
	}
	w = request("PUT", "/api/library/scan/settings", `{"enabled":false,"interval_minutes":120,"targets":[1]}`, true)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, dir := range []string{"a/new", "b"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "added.mp4"), []byte("synthetic fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w = request("POST", "/api/library/scan", `{}`, true)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		w = request("GET", "/api/library/scan", "", false)
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if response.Status.Status != "scanning" && response.Status.Status != "cancelling" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan did not finish", response.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if response.Status.Status != "completed" || response.Status.Files != 2 || response.Status.Directories != 4 || response.Settings.Enabled || response.Settings.IntervalMinutes != 120 {
		t.Fatalf("manual scan while auto disabled: %+v", response)
	}
	var files, runs, operations int
	if err := a.DB.QueryRow("SELECT (SELECT count(*) FROM entries WHERE present=1 AND name='added.mp4'),(SELECT count(*) FROM runs),(SELECT count(*) FROM operations)").Scan(&files, &runs, &operations); err != nil || files != 2 || runs != 0 || operations != 0 {
		t.Fatalf("discovery triggered processing: files=%d runs=%d operations=%d %v", files, runs, operations, err)
	}
	var fileID int64
	if err := a.DB.QueryRow("SELECT id FROM entries WHERE name='added.mp4' LIMIT 1").Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if w := request("PUT", "/api/library/scan/settings", fmt.Sprintf(`{"enabled":true,"interval_minutes":60,"targets":[%d]}`, fileID), true); w.Code < 400 {
		t.Fatal("file accepted as scan directory")
	}
}

func TestDirectoryStatePollingDoesNotScanDisk(t *testing.T) {
	a, root, request := scanAPI(t)
	if err := os.WriteFile(filepath.Join(root, "new.mp4"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w := request("GET", "/api/directories/1/state", "", false)
		var state struct {
			Generation string `json:"generation"`
			Refreshing bool   `json:"refreshing"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Generation != "0" || state.Refreshing {
			t.Fatalf("unexpected unindexed state: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM entries WHERE parent=1").Scan(&count); err != nil || count != 0 {
		t.Fatalf("state polling scanned the filesystem: %d %v", count, err)
	}
}
