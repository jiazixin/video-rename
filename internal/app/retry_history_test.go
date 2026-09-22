package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/systemsettings"
)

func TestRetryHistoryRejectsUnsupportedAndKeepsFailures(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.DB.Exec("INSERT INTO events(id,category,action,entry,message,created) VALUES(1,'failure','scan',1,'failed','now'),(2,'failure','automatic',999,'failed','now')"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(`{"ids":[1,2,999],"key":"retry-test-request"}`)))
	var result struct {
		Accepted []int64
		Errors   []string
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Accepted) != 0 || len(result.Errors) != 3 {
		t.Fatal(w.Body.String())
	}
	var n int
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='failure'").Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(`{"ids":[2],"key":"retry-test-request"}`))
	r.Header.Set("Content-Type", "application/json")
	a.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated retry accepted")
	}
}

func TestRetryHistoryBatchesAndRemovesOnlyAfterSuccess(t *testing.T) {
	root := t.TempDir()
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Workers: 2, Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tools := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err = os.WriteFile(filepath.Join(tools, name), []byte(`#!/bin/sh
exit 0
`), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	profile, err := (models.Store{DB: a.DB}).Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test", Timeout: "2s", MaxTokens: 128}
	if _, err = (models.Store{DB: a.DB}).Save(profile); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if err = os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.Library.Refresh(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec("INSERT INTO events(category,action,entry,created) SELECT 'failure','automatic',id,'now' FROM entries WHERE kind='file'"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec("INSERT INTO events(category,action,entry,created) SELECT category,action,entry,created FROM events LIMIT 1"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(`{"ids":[1,2,3],"key":"batch-retry-request"}`)))
	var result struct {
		Accepted []int64
		Runs     []int64
		Errors   []string
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 3 || len(result.Runs) != 1 || len(result.Errors) != 0 {
		t.Fatal(w.Body.String())
	}
	var count int
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='failure'").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	if err = (systemsettings.Store{DB: a.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	a.Jobs.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
		return "new-title", nil
	}
	a.Jobs.Execute(context.Background(), result.Runs[0])
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='failure'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='success'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
