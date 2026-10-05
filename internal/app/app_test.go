package app

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"video-rename/internal/config"
)

func TestMissingRecordFailureUsesReadableMessage(t *testing.T) {
	for _, err := range []error{sql.ErrNoRows, fmt.Errorf("load record: %w", sql.ErrNoRows)} {
		w := httptest.NewRecorder()
		failure(w, err)
		var result map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 404 || result["code"] != "NOT_FOUND" || result["request_id"] == "" || result["message"] != "请求的记录不存在或已删除，请刷新页面后重试" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestHistoryDateFiltersCompareInstants(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, stamp := range []string{"2026-10-02T00:00:00Z", "2026-10-02T00:00:01.5Z", "2026-10-02T00:00:02Z"} {
		if _, err := a.DB.Exec("INSERT INTO events(category,action,created) VALUES('success','manual',?)", stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, bounds := range [][2]string{
		{"2026-10-02T00:00:00.000Z", "2026-10-02T00:00:02.000Z"},
		{"2026-10-02T08:00:00+08:00", "2026-10-02T08:00:02+08:00"},
		{"2026-10-01", "2026-10-03"},
	} {
		query := url.Values{"category": {"success"}, "page": {"1"}, "from": {bounds[0]}, "to": {bounds[1]}}
		w := httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?"+query.Encode(), nil))
		var result struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || result.Total != 3 {
			t.Fatalf("bounds %v: status=%d body=%s", bounds, w.Code, w.Body.String())
		}
	}
	for _, key := range []string{"from", "to"} {
		w := httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?category=success&"+key+"=invalid", nil))
		if w.Code != 400 {
			t.Fatalf("invalid %s: status=%d body=%s", key, w.Code, w.Body.String())
		}
	}
}

func TestStaticSPAAndProtectedAPI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	os.Mkdir(root, 0700)
	cfg := &config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler()
	for _, p := range []string{"/", "/login", "/library"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/system", nil))
	if w.Code != 401 {
		t.Fatal("unprotected", w.Code)
	}
}

func TestDataDirectoryPersistenceAndShortPasswordLogin(t *testing.T) {
	cfg := &config.Config{Path: t.TempDir(), Storage: filepath.Join(t.TempDir(), "state", "nested"), Auth: config.AuthConfig{Username: "admin", Password: "x"}}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"data.sqlite", "data.sqlite.key"} {
		if _, err := os.Stat(filepath.Join(cfg.Storage, name)); err != nil {
			a.Close()
			t.Fatal(err)
		}
	}
	if _, err = a.DB.Exec("INSERT INTO events(category,action,message,created) VALUES('success','manual','retained','now')"); err != nil {
		a.Close()
		t.Fatal(err)
	}
	secret, err := a.DB.Seal("retained-key")
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	plain, err := a.DB.Unseal(secret)
	if err != nil || plain != "retained-key" {
		t.Fatal("key not preserved", err)
	}
	var count int
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE message='retained'").Scan(&count); err != nil || count != 1 {
		t.Fatal("data not preserved", err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("short password login: %d %s", w.Code, w.Body.String())
	}
}
