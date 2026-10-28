package app

import (
	"database/sql"
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func TestRunPauseResumeAPI(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	if _, err = storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[1]',1,'queued','2026-09-29T00:00:00.000Z','pause-api')")
	}); err != nil {
		t.Fatal(err)
	}
	var cookies []*http.Cookie
	request := func(path, body string, sameSite bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := jsonRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, action := range []string{"pause", "resume"} {
		if w := request("/api/runs/1/"+action, `{"confirm":true}`, true); w.Code != 401 && w.Code != 403 {
			t.Fatal("unauthenticated operation", w.Code)
		}
	}
	w := request("/api/auth/login", `{"username":"admin","password":"test-password"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies = w.Result().Cookies()
	var session map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"pause", "resume"} {
		path := "/api/runs/1/" + action
		if w = request(path, `{"confirm":true}`, false); w.Code != 403 {
			t.Fatal("cross-site write accepted", w.Code)
		}
		if w = request(path, `{}`, true); w.Code != 400 {
			t.Fatal("missing confirmation", w.Code)
		}
		if w = request("/api/runs/0/"+action, `{"confirm":true}`, true); w.Code != 400 {
			t.Fatal("invalid ID", w.Code)
		}
		if w = request(path, `{"confirm":true}`, true); w.Code != http.StatusNoContent {
			t.Fatal(w.Code, w.Body.String())
		}
		run, err := a.Jobs.Get(t.Context(), 1)
		expected := "paused"
		if action == "resume" {
			expected = "queued"
		}
		if err != nil || run.Status != expected {
			t.Fatal(run, err)
		}
	}
	if w = request("/api/runs/1/resume", `{"confirm":true}`, true); w.Code < 400 {
		t.Fatal("resumed unpaused run")
	}
}
