package app

import (
	"encoding/json/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/library"
)

func TestDirectoryProcessingStatusAPI(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"1.jpg", "2.jpg", "photo2.jpg", "photo10.jpg", "video.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"2.jpg": "marked", "photo2.jpg": "manual", "photo10.jpg": "manual", "video.mp4": "automatic"} {
		if _, err := a.DB.Exec(`INSERT INTO marks(identity,size,mtime,source) SELECT identity,size,mtime,? FROM entries WHERE name=?`, source, name); err != nil {
			t.Fatal(err)
		}
	}
	handler := a.Handler()
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password-123"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(login, loginRequest)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body.String())
	}
	var session map[string]string
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", session["csrf"])
		for _, cookie := range login.Result().Cookies() {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{
		"/api/directories/1/entries?page=1&kind=image&status=manual&sort=status_desc",
		"/api/directories/1/entries?kind=image&status=manual&sort=status_desc",
	} {
		w := request("GET", path, "")
		var page library.Page
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].Name != "photo10.jpg" || page.Items[1].Name != "photo2.jpg" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := request("GET", "/api/directories/1/entries?page=1&kind=image&status=processed&sort=status_asc", "")
	var page library.Page
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 3 || page.Items[0].Name != "2.jpg" {
		t.Fatalf("aggregate filter: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/directories/1/numberings/preview", `{"media":"image","query":"photo","status":"manual","sort":"status_desc"}`)
	var preview library.Numbering
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Total != 2 || len(preview.Items) != 2 || preview.Items[0].Old != "photo10.jpg" || preview.Items[0].New != "3.jpg" || preview.Items[1].Old != "photo2.jpg" || preview.Items[1].New != "4.jpg" {
		t.Fatalf("filtered numbering request: %d %s", w.Code, w.Body.String())
	}
	for _, endpoint := range []struct{ method, path, body string }{
		{"GET", "/api/directories/1/entries?page=1&status=invalid", ""},
		{"POST", "/api/directories/1/numberings/preview", `{"media":"image","status":"invalid","sort":"status_asc"}`},
	} {
		w := request(endpoint.method, endpoint.path, endpoint.body)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "无效的处理状态") {
			t.Fatalf("invalid status: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestDirectoryStatusAPIFiltersCompletedSubtrees(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"completed/nested", "partial", "unknown"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"completed/nested/clip.mp4", "partial/one.mp4", "partial/two.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range []string{"", "completed", "nested", "partial"} {
		var id int64
		if err := a.DB.Read().QueryRow("SELECT id FROM entries WHERE name=? AND present=1", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := a.Library.Refresh(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.DB.Exec(`INSERT INTO marks(identity,size,mtime,source) SELECT identity,size,mtime,'manual' FROM entries WHERE kind='file' AND name IN ('clip.mp4','one.mp4')`); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()
	login := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password-123"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(login, r)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body.String())
	}
	for _, test := range []struct {
		path  string
		names []string
	}{
		{"/api/directories/1/entries?page=99&limit=50&status=unprocessed", []string{"partial", "unknown"}},
		{"/api/directories/1/entries?limit=50&status=unprocessed", []string{"partial", "unknown"}},
		{"/api/directories/1/entries?page=1&status=processed", []string{"completed"}},
		{"/api/directories/1/entries?page=1&q=completed&status=unprocessed", nil},
	} {
		r := httptest.NewRequest("GET", test.path, nil)
		for _, cookie := range login.Result().Cookies() {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var page library.Page
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != len(test.names) || len(page.Items) != len(test.names) {
			t.Fatalf("%s: %d %s", test.path, w.Code, w.Body.String())
		}
		for i, name := range test.names {
			if page.Items[i].Name != name || page.Items[i].Progress == nil {
				t.Fatalf("%s: missing filtered progress: %+v", test.path, page.Items)
			}
			if name == "completed" && (page.Items[i].Progress.Processed != 1 || page.Items[i].Progress.Total != 1 || page.Items[i].Progress.Unknown != 0) {
				t.Fatalf("completed state: %+v", page.Items[i].Progress)
			}
			if name == "partial" && (page.Items[i].Progress.Processed != 1 || page.Items[i].Progress.Total != 2) {
				t.Fatalf("partial state: %+v", page.Items[i].Progress)
			}
			if name == "unknown" && page.Items[i].Progress.Unknown != 1 {
				t.Fatalf("unknown state: %+v", page.Items[i].Progress)
			}
		}
	}
}
