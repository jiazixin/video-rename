package app

import (
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/library"
)

func TestImageNumberingAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "photo.PNG"), []byte("picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(t.Context(), &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	var cookies []*http.Cookie
	request := func(method, path, body string, sameSite bool) *httptest.ResponseRecorder {
		r := jsonRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/directories/1/numberings/preview", `{}`, true); w.Code != 401 && w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, true)
	cookies = w.Result().Cookies()
	var session map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if w = request("POST", "/api/directories/1/numberings/preview", `{}`, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	w = request("POST", "/api/directories/1/numberings/preview", `{"media":"image","query":"","sort":"name_desc"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var b library.Numbering
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil || len(b.Items) != 1 || b.Items[0].New != "1.png" {
		t.Fatal(b, err)
	}
	if strings.Contains(w.Body.String(), "identity") || strings.Contains(w.Body.String(), "temp_name") {
		t.Fatal("internal journal exposed")
	}
	for _, action := range []string{"apply", "restore-preview", "recover"} {
		if w = request("POST", "/api/numberings/"+b.ID+"/"+action, `{}`, false); w.Code != 403 {
			t.Fatal("cross-site write accepted", action, w.Code)
		}
	}
	for range 2 {
		w = request("POST", "/api/numberings/"+b.ID+"/apply", `{}`, true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = request("GET", "/api/directories/1/numberings?media=image", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "committed") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/api/numberings/"+b.ID+"/restore-preview", `{}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	w = request("POST", "/api/numberings/"+b.ID+"/apply", `{}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if raw, err := os.ReadFile(filepath.Join(root, "photo.PNG")); err != nil || string(raw) != "picture" {
		t.Fatal(string(raw), err)
	}
}

func TestNumberingSettingsAPI(t *testing.T) {
	root := t.TempDir()
	a, err := New(t.Context(), &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	handler := a.Handler()
	loginReq := jsonRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, loginReq)
	var session map[string]string
	json.Unmarshal(login.Body.Bytes(), &session)
	request := func(method, path, body string, sameSite bool) *httptest.ResponseRecorder {
		r := jsonRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range login.Result().Cookies() {
			r.AddCookie(c)
		}
		if !sameSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/numbering/settings", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"padding":1`) || !strings.Contains(w.Body.String(), `"lowercase":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("PUT", "/api/numbering/settings", `{"image":{"padding":3,"lowercase":true,"prefix":"IMG_","compact":false},"audio":{"padding":1,"lowercase":false},"shared":true}`, false); w.Code != 403 {
		t.Fatal("cross-site write accepted", w.Code)
	}
	if w = request("PUT", "/api/numbering/settings", `{"image":{"padding":3,"lowercase":true,"prefix":"IMG_","compact":false},"audio":{"padding":1,"lowercase":false},"shared":true}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/api/numbering/settings", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"prefix":"IMG_"`) || !strings.Contains(w.Body.String(), `"shared":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("PUT", "/api/numbering/settings", `{"image":{"padding":0,"lowercase":true},"audio":{"padding":1,"lowercase":true},"shared":false}`, true); w.Code == 200 {
		t.Fatal("invalid padding accepted")
	}
}

func TestAudioNumberingAPI(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "track.MP3"), []byte("audio"), 0o600)
	os.WriteFile(filepath.Join(root, "cover.png"), []byte("cover"), 0o600)
	a, err := New(t.Context(), &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/directories/1/numberings?media=video", nil))
	if unauth.Code != 401 {
		t.Fatal("unprotected", unauth.Code)
	}
	loginReq := jsonRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, loginReq)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := jsonRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range login.Result().Cookies() {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/directories/1/numberings/preview", `{"media":"video","query":"","sort":"name_asc"}`); w.Code != 400 {
		t.Fatal("invalid media accepted", w.Code)
	}
	w := request("POST", "/api/directories/1/numberings/preview", `{"media":"audio","query":"","sort":"name_asc"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var b library.Numbering
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil || len(b.Items) != 1 || b.Items[0].New != "1.mp3" {
		t.Fatal(b, err)
	}
	if w = request("POST", "/api/numberings/"+b.ID+"/apply", `{}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = os.Stat(filepath.Join(root, "cover.png")); err != nil {
		t.Fatal("image touched by audio numbering", err)
	}
	if w = request("GET", "/api/directories/1/numberings?media=audio", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "committed") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/api/directories/1/numberings?media=image", ""); w.Code != 200 || strings.Contains(w.Body.String(), "committed") {
		t.Fatal("audio batch leaked into image history", w.Code)
	}
}

func TestNumberingFailurePreservesWrappedDiagnostics(t *testing.T) {
	err := fmt.Errorf("parent/child：%w", &domain.JobError{Code: domain.CodeRenameConflict, Message: "目标被占用", ProcessingLog: "原文件名：photo.jpg\n目标类型：directory"})
	w := httptest.NewRecorder()
	numberingFailure(w, httptest.NewRequest("POST", "/api/numberings/x/apply", nil), err)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				ProcessingLog string `json:"processing_log"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusConflict || body.Error.Code != "rename_conflict" || !strings.Contains(body.Error.Message, "parent/child") || !strings.Contains(body.Error.Details.ProcessingLog, "目标类型：directory") {
		t.Fatal(w.Code, body)
	}
}
