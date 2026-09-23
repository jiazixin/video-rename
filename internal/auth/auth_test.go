package auth

import (
	"encoding/json/v2"
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

func TestLoginRateLimitCountsFailuresOnly(t *testing.T) {
	db, e := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := New(db, config.AuthConfig{Username: "admin", Password: "test-password-123"})
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	login := func(user, pass string) int {
		r := httptest.NewRequest("POST", "http://example.test/api/auth/login", strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// 成功登录不计入限流:合法用户短时间内反复登录不受影响。
	for i := range 12 {
		if code := login("admin", "test-password-123"); code != 200 {
			t.Fatalf("成功登录 #%d 不应被限流, got %d", i+1, code)
		}
	}
	// 只有连续失败才计入:10 次失败后进入限流。
	for i := range 10 {
		if code := login("admin", "wrong-password"); code != 401 {
			t.Fatalf("失败登录 #%d 应为 401, got %d", i+1, code)
		}
	}
	if code := login("admin", "test-password-123"); code != 429 {
		t.Fatalf("达到失败上限后应为 429, got %d", code)
	}
}
