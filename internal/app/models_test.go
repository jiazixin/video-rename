package app

import (
	"bytes"
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
)

func TestModelAPISecretsAndCrossOrigin(t *testing.T) {
	cfg := &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}
	a, err := New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler()
	var cookie *http.Cookie
	call := func(method, path string, payload any, sameSite bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		r := httptest.NewRequest(method, "http://example.com"+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://example.com")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "test-password-123"}, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie = w.Result().Cookies()[0]
	var auth map[string]string
	json.Unmarshal(w.Body.Bytes(), &auth)
	w = call("POST", "/api/prompts", map[string]any{"title": "测试提示词", "body": "title only"}, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	data := map[string]any{"title": "private profile", "prompt_id": created["id"], "provider": "custom", "protocol": "openai", "base_url": "http://localhost:8080/v1", "model": "vision", "temperature": 0, "max_tokens": 1024, "timeout": "10s", "api_key": "api-private-test-key"}
	if w = call("POST", "/api/model-configs", data, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	w = call("POST", "/api/model-configs", data, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "api-private-test-key") || strings.Contains(w.Body.String(), `"api_key"`) {
		t.Fatal("save leaked secret")
	}
	w = call("GET", "/api/model-configs", nil, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "api-private-test-key") {
		t.Fatal("list leaked secret")
	}
}
