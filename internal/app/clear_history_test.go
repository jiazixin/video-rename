package app

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
)

func TestClearHistoryCategoryIsolation(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, category := range []string{"success", "failure"} {
		if _, err = a.DB.Exec("INSERT INTO events(category,action,message,created) VALUES(?,'manual','test','now')", category); err != nil {
			t.Fatal(err)
		}
	}
	var success, failureID int64
	a.DB.QueryRow("SELECT id FROM events WHERE category='success'").Scan(&success)
	a.DB.QueryRow("SELECT id FROM events WHERE category='failure'").Scan(&failureID)
	request := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.clearHistory(w, jsonRequest("POST", "/api/logs/clear", strings.NewReader(body)))
		return w
	}
	if w := request(`{"category":"failure","all":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var n int
	a.DB.QueryRow("SELECT count(*) FROM events WHERE category='success'").Scan(&n)
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
