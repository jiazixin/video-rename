package app

import (
	"encoding/json/v2"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/notifications"
)

func TestNotificationEndpointsRequireSessionAndSameSite(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	for _, endpoint := range []struct{ method, url string }{{"GET", "/api/notifications"}, {"GET", "/api/notifications/history"}, {"PUT", "/api/notifications/webhook"}, {"POST", "/api/notifications/webhook/test"}} {
		w := httptest.NewRecorder()
		r := jsonRequest(endpoint.method, endpoint.url, strings.NewReader(`{"enabled":true,"endpoint":"http://127.0.0.1/webhook"}`))
		r.Header.Set("Content-Type", "application/json")
		a.Handler().ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal(endpoint, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r := jsonRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password-123"}`))
	r.Header.Set("Content-Type", "application/json")
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	cookies := w.Result().Cookies()
	for _, url := range []string{"/api/notifications/history?page=99&size=100", "/api/notifications/history?size=200", "/api/notifications/history?page=0&size=-1"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest("GET", url, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		a.Handler().ServeHTTP(w, r)
		var page notifications.HistoryPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.Page != 1 || page.Items == nil || page.Total != 0 {
			t.Fatal("invalid history page", w.Code, w.Body.String(), err)
		}
	}
	for _, sameSite := range []bool{false, true} {
		w = httptest.NewRecorder()
		r = jsonRequest("PUT", "/api/notifications/webhook", strings.NewReader(`{"enabled":true,"endpoint":"file:///etc/passwd"}`))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		a.Handler().ServeHTTP(w, r)
		if !sameSite && w.Code != 403 {
			t.Fatal("cross-site write accepted", w.Code)
		}
		if sameSite && w.Code == 200 {
			t.Fatal("file URL accepted")
		}
	}
}
