package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"video-rename/internal/config"
	"video-rename/internal/library"
)

func TestImageNumberingAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "photo.PNG"), []byte("picture"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Library.Refresh(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	var cookies []*http.Cookie
	csrf := ""
	request := func(method, path, body string, token bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		if token {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/directories/1/image-numberings/preview", `{}`, false); w.Code != 401 && w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, false)
	cookies = w.Result().Cookies()
	var session map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	csrf = session["csrf"]
	if w = request("POST", "/api/directories/1/image-numberings/preview", `{}`, false); w.Code != 403 {
		t.Fatal("no CSRF accepted", w.Code)
	}
	w = request("POST", "/api/directories/1/image-numberings/preview", `{"query":"","sort":"name_desc"}`, true)
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
		if w = request("POST", "/api/image-numberings/"+b.ID+"/"+action, `{}`, false); w.Code != 403 {
			t.Fatal("no CSRF accepted", action, w.Code)
		}
	}
	for range 2 {
		w = request("POST", "/api/image-numberings/"+b.ID+"/apply", `{}`, true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = request("GET", "/api/directories/1/image-numberings", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "committed") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("POST", "/api/image-numberings/"+b.ID+"/restore-preview", `{}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	w = request("POST", "/api/image-numberings/"+b.ID+"/apply", `{}`, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if raw, err := os.ReadFile(filepath.Join(root, "photo.PNG")); err != nil || string(raw) != "picture" {
		t.Fatal(string(raw), err)
	}
}
