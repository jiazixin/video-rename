package app

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
)

func TestServiceEndpointsAndStrictLogin(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	a.build.Version = "v1.0.0-test"
	handler := a.Handler()
	for _, tc := range []struct {
		name, method, path, body, contentType string
		status                                int
		contains                              string
	}{
		{"health", "GET", "/healthz", "", "", http.StatusOK, `{"status":"ok"}`},
		{"info", "GET", "/api/info", "", "", http.StatusOK, `"version":"v1.0.0-test"`},
		{"unknown route", "GET", "/api/unknown", "", "", http.StatusUnauthorized, `"code":"unauthenticated"`},
		{"health method", "POST", "/healthz", "", "", http.StatusMethodNotAllowed, ``},
		{"unknown member", "POST", "/api/auth/login", `{"username":"admin","password":"test-password","extra":true}`, "application/json", http.StatusBadRequest, `"code":"invalid_argument"`},
		{"null", "POST", "/api/auth/login", `null`, "application/json", http.StatusBadRequest, `"reason":"not_object"`},
		{"trailing object", "POST", "/api/auth/login", `{"username":"admin","password":"test-password"}{}`, "application/json", http.StatusBadRequest, `"reason":"trailing_data"`},
		{"invalid media type", "POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, "application/jsonp", http.StatusUnsupportedMediaType, `"code":"unsupported_media_type"`},
		{"charset", "POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, "application/json; charset=utf-8", http.StatusOK, `"username":"admin"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://example.test"+tc.path, strings.NewReader(tc.body))
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) || w.Header().Get("X-Request-ID") == "" {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
