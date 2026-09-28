package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"video-rename/internal/config"
)

func TestRunPauseResumeAPI(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.DB.Exec("INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[1]',1,'queued','2026-09-29T00:00:00Z','pause-api')"); err != nil {
		t.Fatal(err)
	}
	var cookies []*http.Cookie
	var csrf string
	request := func(path, body string, token bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		if token {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, action := range []string{"pause", "resume"} {
		if w := request("/api/runs/1/"+action, `{"confirm":true}`, false); w.Code != 401 && w.Code != 403 {
			t.Fatal("unauthenticated operation", w.Code)
		}
	}
	w := request("/api/auth/login", `{"username":"admin","password":"test-password"}`, false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies = w.Result().Cookies()
	var session map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	csrf = session["csrf"]
	for _, action := range []string{"pause", "resume"} {
		path := "/api/runs/1/" + action
		if w = request(path, `{"confirm":true}`, false); w.Code != 403 {
			t.Fatal("missing CSRF", w.Code)
		}
		if w = request(path, `{}`, true); w.Code != 400 {
			t.Fatal("missing confirmation", w.Code)
		}
		if w = request("/api/runs/0/"+action, `{"confirm":true}`, true); w.Code != 400 {
			t.Fatal("invalid ID", w.Code)
		}
		if w = request(path, `{"confirm":true}`, true); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		run, err := a.Jobs.Get(1)
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
