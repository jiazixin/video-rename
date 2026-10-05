package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"video-rename/internal/config"
	"video-rename/internal/systemsettings"
)

func TestSystemSettingsAPI(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var cookies []*http.Cookie
	csrf := ""
	request := func(method, path, body string, token bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if token {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{"GET", "PUT"} {
		if w := request(method, "/api/system/settings", "{}", false); w.Code != 401 && w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	w := request("POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, false)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies = w.Result().Cookies()
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	csrf = session["csrf"]
	w = request("GET", "/api/system/settings", "", false)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var response struct {
		Settings systemsettings.Settings `json:"settings"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Settings != systemsettings.Default() {
		t.Fatal(response)
	}
	if w = request("POST", "/api/system/cache/clear", `{"confirm":true}`, false); w.Code != 403 {
		t.Fatal("clear without CSRF", w.Code)
	}
	if w = request("POST", "/api/system/cache/clear", `{"confirm":false}`, true); w.Code < 400 {
		t.Fatal("clear without confirmation", w.Code)
	}
	if w = request("POST", "/api/system/cache/clear", `{"confirm":true}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	value := systemsettings.Settings{ExtractWorkers: 3, CacheGB: 2}
	raw, _ := json.Marshal(value)
	if w = request("PUT", "/api/system/settings", string(raw), false); w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	if w = request("PUT", "/api/system/settings", string(raw), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"unknown":true}`, `{"extract_workers":0,"cache_gb":10}`, `{"extract_workers":33,"cache_gb":10}`, `{"extract_workers":8,"cache_gb":0}`, `{"extract_workers":8,"cache_gb":10,"stability_seconds":-1}`, `{"extract_workers":8,"cache_gb":10,"stability_seconds":3601}`, `{"extract_workers":8,"cache_gb":10,"stability_seconds":1.5}`} {
		if w = request("PUT", "/api/system/settings", body, true); w.Code < 400 {
			t.Fatal("invalid accepted", body)
		}
	}

	w = request("GET", "/api/system/settings", "", false)
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Settings != value {
		t.Fatal(response, err)
	}
}
