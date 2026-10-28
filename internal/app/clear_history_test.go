package app

import (
	"database/sql"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func TestClearHistoryCategoryIsolation(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	for _, category := range []string{"success", "failure"} {
		if _, err = storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), "INSERT INTO events(category,action,message,created) VALUES(?,'manual','test','2026-10-01T00:00:00.000Z')", category)
		}); err != nil {
			t.Fatal(err)
		}
	}
	var success, failureID int64
	a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM events WHERE category='success'").Scan(&success)
	})
	a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM events WHERE category='failure'").Scan(&failureID)
	})
	request := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.clearHistory(w, jsonRequest("POST", "/api/logs/clear", strings.NewReader(body)))
		return w
	}
	if w := request(`{"category":"failure","all":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var n int
	a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events WHERE category='success'").Scan(&n)
	})
	if n != 1 {
		t.Fatal("cleared other category")
	}
	if w := request(`{"category":"success","ids":[]}`); w.Code != 400 {
		t.Fatal("empty selection accepted")
	}
	w := httptest.NewRecorder()
	r := jsonRequest("POST", "/api/logs/clear", strings.NewReader(`{"category":"success","all":true}`))
	r.Header.Set("Content-Type", "application/json")
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated clear")
	}
}
