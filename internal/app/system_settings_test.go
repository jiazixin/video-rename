package app

import (
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/systemsetting"
)

func TestSystemSettingsAPI(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	var cookies []*http.Cookie
	request := func(method, path, body string, sameSite bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := jsonRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, method := range []string{"GET", "PUT"} {
		if w := request(method, "/api/system/settings", "{}", true); w.Code != 401 && w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	w := request("POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies = w.Result().Cookies()
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	w = request("GET", "/api/system/settings", "", true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var response struct {
		Settings systemsetting.Settings `json:"settings"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Settings != systemsetting.Default() {
		t.Fatal(response)
	}
	if w = request("POST", "/api/system/cache/clear", `{"confirm":true}`, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	if w = request("POST", "/api/system/cache/clear", `{"confirm":false}`, true); w.Code < 400 {
		t.Fatal("clear without confirmation", w.Code)
	}
	if w = request("POST", "/api/system/cache/clear", `{"confirm":true}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	value := systemsetting.Default()
	value.ExtractWorkers, value.CacheGB = 3, 2
	value.RunningBatchLimit, value.WaitingBatchLimit = 64, 5000
	raw, _ := json.Marshal(value)
	if w = request("PUT", "/api/system/settings", string(raw), false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	if w = request("PUT", "/api/system/settings", string(raw), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"unknown":true}`, `{"extract_workers":8,"cache_gb":10,"running_batch_limit":0}`, `{"extract_workers":8,"cache_gb":10,"running_batch_limit":257}`, `{"extract_workers":8,"cache_gb":10,"waiting_batch_limit":0}`, `{"extract_workers":8,"cache_gb":10,"waiting_batch_limit":100001}`, `{"extract_workers":8,"cache_gb":10,"running_batch_limit":1.5}`, `{"extract_workers":0,"cache_gb":10}`, `{"extract_workers":33,"cache_gb":10}`, `{"extract_workers":8,"cache_gb":0}`, `{"extract_workers":8,"cache_gb":10,"stability_seconds":-1}`, `{"extract_workers":8,"cache_gb":10,"stability_seconds":3601}`, `{"extract_workers":8,"cache_gb":10,"stability_seconds":1.5}`} {
		if w = request("PUT", "/api/system/settings", body, true); w.Code < 400 {
			t.Fatal("invalid accepted", body)
		}
	}

	w = request("GET", "/api/system/settings", "", true)
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Settings != value {
		t.Fatal(response, err)
	}
}
