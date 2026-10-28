package app

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func TestMissingRecordFailureUsesReadableMessage(t *testing.T) {
	for _, err := range []error{sql.ErrNoRows, fmt.Errorf("load record: %w", sql.ErrNoRows)} {
		w := httptest.NewRecorder()
		writeError(w, httptest.NewRequest("GET", "/api/entries/1", nil), err)
		var result struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 404 || result.Error.Code != "not_found" || result.Error.Message == "" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestHistoryDateFiltersCompareInstants(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	for _, stamp := range []string{"2026-10-02T00:00:00Z", "2026-10-02T00:00:01.5Z", "2026-10-02T00:00:02Z"} {
		if _, err := storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), "INSERT INTO events(category,action,created) VALUES('success','manual',?)", stamp)
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, bounds := range [][2]string{
		{"2026-10-02T00:00:00.000Z", "2026-10-02T00:00:02.000Z"},
		{"2026-10-02T08:00:00+08:00", "2026-10-02T08:00:02+08:00"},
		{"2026-10-01", "2026-10-03"},
	} {
		query := url.Values{"category": {"success"}, "page": {"1"}, "from": {bounds[0]}, "to": {bounds[1]}}
		w := httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?"+query.Encode(), nil))
		var result struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || result.Total != 3 {
			t.Fatalf("bounds %v: status=%d body=%s", bounds, w.Code, w.Body.String())
		}
	}
	for _, key := range []string{"from", "to"} {
		w := httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?category=success&"+key+"=invalid", nil))
		if w.Code != 400 {
			t.Fatalf("invalid %s: status=%d body=%s", key, w.Code, w.Body.String())
		}
	}
}

func TestStaticSPAAndProtectedAPI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	os.Mkdir(root, 0o700)
	cfg := &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}
	a, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	h := a.Handler()
	for _, p := range []string{"/", "/login", "/library", "/settings/models"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Fatalf("%s: got CSP %q, want %q", p, got, contentSecurityPolicy)
		}
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest("GET", "/assets/missing.js", nil))
	if missing.Code != 404 {
		t.Fatalf("missing asset status=%d", missing.Code)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/system", nil))
	if w.Code != 401 {
		t.Fatal("unprotected", w.Code)
	}
}

func TestDataDirectoryPersistenceAndShortPasswordLogin(t *testing.T) {
	cfg := &config.Config{Path: t.TempDir(), Storage: filepath.Join(t.TempDir(), "state", "nested"), Auth: config.AuthConfig{Username: "admin", Password: "x"}}
	a, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"data.sqlite", "data.sqlite.key"} {
		if _, err := os.Stat(filepath.Join(cfg.Storage, name)); err != nil {
			shutdown(t, a)
			t.Fatal(err)
		}
	}
	if _, err = storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO events(category,action,message,created) VALUES('success','manual','retained','2026-10-01T00:00:00.000Z')")
	}); err != nil {
		shutdown(t, a)
		t.Fatal(err)
	}
	secret, err := a.db.Seal("retained-key")
	if err != nil {
		shutdown(t, a)
		t.Fatal(err)
	}
	if err = a.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	a, err = New(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	plain, err := a.db.Unseal(secret)
	if err != nil || plain != "retained-key" {
		t.Fatal("key not preserved", err)
	}
	var count int
	if err = a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events WHERE message='retained'").Scan(&count)
	}); err != nil || count != 1 {
		t.Fatal("data not preserved", err)
	}
	w := httptest.NewRecorder()
	r := jsonRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("short password login: %d %s", w.Code, w.Body.String())
	}
}

func TestShutdownTimeoutLeavesStoresOpen(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	release := make(chan struct{})
	defer close(release)
	a.wg.Go(func() { <-release })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := a.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if err := a.db.View(t.Context(), func(tx *sql.Tx) error {
		var count int
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events").Scan(&count)
	}); err != nil {
		t.Fatalf("store closed while worker still active: %v", err)
	}
}

// shutdown stops a test app. Cleanup runs after t.Context is cancelled, so it
// waits without a deadline.
func shutdown(t testing.TB, a *App) {
	t.Helper()
	if err := a.Shutdown(context.Background()); err != nil {
		t.Error(err)
	}
}
