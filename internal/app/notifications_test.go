package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"video-rename/internal/config"
)

func TestNotificationEndpointsRequireSessionAndCSRF(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, endpoint := range []struct{ method, url string }{{"GET", "/api/notifications"}, {"GET", "/api/notifications/history"}, {"PUT", "/api/notifications/webhook"}, {"POST", "/api/notifications/webhook/test"}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(endpoint.method, endpoint.url, strings.NewReader(`{"enabled":true,"endpoint":"http://127.0.0.1/webhook"}`))
		r.Header.Set("Content-Type", "application/json")
		a.Handler().ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal(endpoint, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password-123"}`))
	r.Header.Set("Content-Type", "application/json")
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	cookies := w.Result().Cookies()
	for _, csrf := range []bool{false, true} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest("PUT", "/api/notifications/webhook", strings.NewReader(`{"enabled":true,"endpoint":"file:///etc/passwd"}`))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if csrf {
			r.Header.Set("X-CSRF-Token", session["csrf"])
		}
		a.Handler().ServeHTTP(w, r)
		if !csrf && w.Code != 403 {
			t.Fatal("missing CSRF accepted", w.Code)
		}
		if csrf && w.Code == 200 {
			t.Fatal("file URL accepted")
		}
	}
}
