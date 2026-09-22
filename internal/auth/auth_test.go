package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func TestSessionCSRFAndLogout(t *testing.T) {
	db, e := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := New(db, config.AuthConfig{Username: "admin", Password: "test-password-123"})
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	call := func(path, method, body string, c *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("/api/data", "GET", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call("/api/auth/login", "POST", `{"username":"admin","password":"test-password-123"}`, nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("missing protected cookie")
	}
	var sess map[string]string
	json.Unmarshal(w.Body.Bytes(), &sess)
	if w = call("/api/data", "POST", "{}", cookies[0], "", ""); w.Code != 403 {
		t.Fatal("missing csrf allowed")
	}
	if w = call("/api/data", "POST", "{}", cookies[0], sess["csrf"], "https://evil.test"); w.Code != 403 {
		t.Fatal("cross origin allowed")
	}
	if w = call("/api/data", "POST", "{}", cookies[0], sess["csrf"], ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w = call("/api/auth/logout", "POST", "{}", cookies[0], sess["csrf"], ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call("/api/data", "GET", "", cookies[0], "", ""); w.Code != 401 {
		t.Fatal("revoked cookie accepted")
	}
}
