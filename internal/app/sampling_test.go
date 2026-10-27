package app

import (
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
)

func TestSamplingAPI(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
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
		if w := request(method, "/api/sampling", "{}", true); w.Code != 401 && w.Code != 403 {
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
	w = request("GET", "/api/sampling", "", true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var value config.FrameSettings
	if err = json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	value.Sampling.Strategy = "duration"
	value.Sampling.MaxFrames = 32
	value.Images.MaxDimension = 1024
	raw, _ := json.Marshal(value)
	if w = request("PUT", "/api/sampling", string(raw), false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	if w = request("PUT", "/api/sampling", string(raw), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"unknown":true}`, strings.Replace(string(raw), `"max_frames":32`, `"max_frames":33`, 1), strings.Replace(string(raw), `"skip_end_ratio":0.05`, `"skip_end_ratio":0.99`, 1)} {
		if w = request("PUT", "/api/sampling", body, true); w.Code < 400 {
			t.Fatal("invalid accepted", body)
		}
	}
	w = request("GET", "/api/sampling", "", true)
	if err = json.Unmarshal(w.Body.Bytes(), &value); err != nil || value.Images.MaxDimension != 1024 || value.Sampling.MaxFrames != 32 {
		t.Fatal(value, err)
	}
}
