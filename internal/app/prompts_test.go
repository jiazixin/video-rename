package app

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/systemsetting"
)

func TestPromptAPIFixedLibraryAndGuards(t *testing.T) {
	cfg := &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}
	a, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	h := a.Handler()
	var cookie *http.Cookie
	call := func(method, path string, payload any, sameSite bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		r := jsonRequest(method, "http://example.com"+path, bytes.NewReader(raw))
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
	var w *httptest.ResponseRecorder
	if w = call("GET", "/api/prompts", nil, true); w.Code != 401 {
		t.Fatal("unauthenticated list allowed", w.Code)
	}
	w = call("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "test-password-123"}, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie = w.Result().Cookies()[0]
	var auth map[string]string
	json.Unmarshal(w.Body.Bytes(), &auth)
	if w = call("POST", "/api/prompts", map[string]any{"title": "旅行", "body": "描述旅行风景"}, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	for _, path := range []string{"/api/prompts/not-a-number", "/api/prompts/0", "/api/model-configs/0", "/api/schedules/0"} {
		if w = call("PATCH", path, map[string]any{}, true); w.Code != 400 {
			t.Fatal("invalid update path accepted", path, w.Code, w.Body.String())
		}
	}
	w = call("GET", "/api/prompts", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "标题正文不少于25个中文字符") {
		t.Fatal("default fixed prompt missing", w.Code, w.Body.String())
	}
	var page struct {
		FixedPrompt            string   `json:"fixed_prompt"`
		DefaultFixedPrompt     string   `json:"default_fixed_prompt"`
		FailureKeywords        []string `json:"failure_keywords"`
		DefaultFailureKeywords []string `json:"default_failure_keywords"`
		Prompts                []struct {
			ID      int64  `json:"id"`
			Title   string `json:"title"`
			Version int64  `json:"version"`
		} `json:"prompts"`
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if page.DefaultFixedPrompt != systemsetting.DefaultFixedPrompt || page.FixedPrompt != page.DefaultFixedPrompt {
		t.Fatal("default fixed prompt missing from payload", w.Body.String())
	}
	if !slices.Equal(page.FailureKeywords, systemsetting.DefaultFailureKeywords()) || !slices.Equal(page.DefaultFailureKeywords, page.FailureKeywords) {
		t.Fatal("failure keyword defaults missing from payload", w.Body.String())
	}
	if w = call("PUT", "/api/prompts/failure-keywords", map[string]any{"failure_keywords": []string{}}, false); w.Code != http.StatusForbidden {
		t.Fatal("cross-site failure keyword save accepted", w.Code)
	}
	for _, payload := range []any{
		map[string]any{},
		map[string]any{"failure_keywords": nil},
		map[string]any{"failure_keywords": "sorry"},
		map[string]any{"failure_keywords": []any{123}},
		map[string]any{"failure_keywords": []string{"sorry"}, "unknown": true},
		nil,
		[]string{"sorry"},
	} {
		if w = call("PUT", "/api/prompts/failure-keywords", payload, true); w.Code != http.StatusBadRequest {
			t.Fatal("malformed failure keywords accepted", payload, w.Code, w.Body.String())
		}
	}
	if got, err := (systemsetting.New(a.db)).FailureKeywords(t.Context()); err != nil || !slices.Equal(got, page.FailureKeywords) {
		t.Fatal("rejected request changed rules", got, err)
	}
	for _, keywords := range [][]string{{" "}, {strings.Repeat("字", 201)}, make([]string, 101)} {
		if w = call("PUT", "/api/prompts/failure-keywords", map[string]any{"failure_keywords": keywords}, true); w.Code != http.StatusBadRequest {
			t.Fatal("invalid failure keywords accepted", keywords, w.Code, w.Body.String())
		}
	}
	for _, keywords := range [][]string{{"  自定义失败句子  ", "Sorry", "SORRY"}, {}, systemsetting.DefaultFailureKeywords()} {
		if w = call("PUT", "/api/prompts/failure-keywords", map[string]any{"failure_keywords": keywords}, true); w.Code != http.StatusOK {
			t.Fatal("failure keyword save failed", w.Code, w.Body.String())
		}
		var response struct {
			FailureKeywords []string `json:"failure_keywords"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		want, err := systemsetting.NormalizeFailureKeywords(keywords)
		if err != nil || response.FailureKeywords == nil || !slices.Equal(response.FailureKeywords, want) {
			t.Fatal("unexpected failure keyword response", w.Body.String(), err)
		}
		w = call("GET", "/api/prompts", nil, true)
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !slices.Equal(response.FailureKeywords, want) {
			t.Fatal("GET did not show saved rules", w.Body.String(), err)
		}
	}
	if len(page.Prompts) != 1 || page.Prompts[0].Title != "默认提示词" {
		t.Fatal("seed prompt missing", w.Body.String())
	}
	if w = call("PUT", "/api/prompts/fixed", map[string]any{"fixed_prompt": "只输出标题。", "unknown": true}, true); w.Code != 400 {
		t.Fatal("unknown member accepted", w.Code)
	}
	if w = call("PUT", "/api/prompts/fixed", map[string]any{"fixed_prompt": "  "}, true); w.Code != 400 {
		t.Fatal("blank fixed prompt accepted", w.Code)
	}
	if w = call("PUT", "/api/prompts/fixed", map[string]any{"fixed_prompt": "只输出标题。"}, true); w.Code != 200 || !strings.Contains(w.Body.String(), "只输出标题。") {
		t.Fatal("fixed prompt save failed", w.Code, w.Body.String())
	}
	if w = call("POST", "/api/prompts", map[string]any{"title": "旅行", "body": "描述旅行风景"}, true); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var entry struct {
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		Version int64  `json:"version"`
	}
	json.Unmarshal(w.Body.Bytes(), &entry)
	if entry.Title != "旅行" || entry.Body != "描述旅行风景" || entry.ID == 0 {
		t.Fatal("created prompt mismatch", w.Body.String())
	}
	profile := map[string]any{"title": "引用配置", "prompt_id": entry.ID, "provider": "custom", "protocol": "openai", "base_url": "http://localhost:8080/v1", "model": "vision", "temperature": 0, "max_tokens": 128, "timeout_ms": 10000}
	if w = call("POST", "/api/model-configs", profile, true); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"prompt_title":"旅行"`) || !strings.Contains(w.Body.String(), "描述旅行风景") {
		t.Fatal("profile did not resolve the referenced prompt", w.Body.String())
	}
	var saved struct {
		ID      int64 `json:"id"`
		Version int64 `json:"version"`
	}
	json.Unmarshal(w.Body.Bytes(), &saved)
	unbound := map[string]any{"title": "缺提示词", "provider": "custom", "protocol": "openai", "base_url": "http://localhost:8080/v1", "model": "vision", "temperature": 0, "max_tokens": 128, "timeout_ms": 10000}
	if w = call("POST", "/api/model-configs", unbound, true); w.Code != 400 || !strings.Contains(w.Body.String(), `"path":"prompt_id"`) {
		t.Fatal("profile without prompt accepted", w.Code, w.Body.String())
	}
	if w = call("DELETE", fmt.Sprintf("/api/prompts/%d", entry.ID), map[string]any{"version": entry.Version}, true); w.Code != 409 || !strings.Contains(w.Body.String(), `"configs":1`) {
		t.Fatal("referenced prompt deleted", w.Code, w.Body.String())
	}
	if w = call("DELETE", fmt.Sprintf("/api/prompts/%d", entry.ID), map[string]any{"version": entry.Version + 1}, true); w.Code != 412 {
		t.Fatal("stale delete accepted", w.Code)
	}
	profile["version"] = saved.Version
	profile["prompt_id"] = page.Prompts[0].ID
	if w = call("PATCH", fmt.Sprintf("/api/model-configs/%d", saved.ID), profile, true); w.Code != 200 {
		t.Fatal("profile could not switch prompt", w.Code, w.Body.String())
	}
	if w = call("DELETE", fmt.Sprintf("/api/prompts/%d", entry.ID), map[string]any{"version": entry.Version}, true); w.Code != http.StatusNoContent {
		t.Fatal("unreferenced prompt delete failed", w.Code, w.Body.String())
	}
}
