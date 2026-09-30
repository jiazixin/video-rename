package app

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
		Accepted []int64  `json:"accepted"`
		Errors   []string `json:"errors"`
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
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
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
		Accepted []int64  `json:"accepted"`
		Runs     []int64  `json:"runs"`
		Errors   []string `json:"errors"`
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
	a.Jobs.Execute(t.Context(), result.Runs[0])
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='failure'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='success'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestRetryAllHistoryUsesChosenModelInOneBatchAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	profile, err := (models.Store{DB: a.DB}).Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test", Timeout: "2s", MaxTokens: 128}
	if _, err = (models.Store{DB: a.DB}).Save(profile); err != nil {
		t.Fatal(err)
	}
	profile.ID = 0
	profile.Title = "重试备用模型"
	profile.Model = "alternative"
	alternative, err := (models.Store{DB: a.DB}).Save(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec("INSERT INTO runs(source,targets,recursive,status,created,request_key,model_config_id) VALUES('manual','[]',0,'failed','now','original-failed-run',1)"); err != nil {
		t.Fatal(err)
	}
	for i := range 205 {
		if err = os.WriteFile(filepath.Join(root, fmt.Sprintf("clip-%03d.mp4", i)), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"INSERT INTO events(category,action,entry,created) SELECT 'failure','automatic',id,'now' FROM entries WHERE kind='file'",
		"INSERT INTO events(category,action,entry,created) SELECT category,action,entry,created FROM events LIMIT 1",
		"INSERT INTO events(category,action,entry,created) VALUES('failure','manual',0,'now'),('success','automatic',0,'now'),('failure','automatic',99999,'now')",
	} {
		if _, err = a.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.DB.Exec("UPDATE events SET run=1 WHERE action='automatic' AND category='failure'"); err != nil {
		t.Fatal(err)
	}
	invalid := httptest.NewRecorder()
	a.retryHistory(invalid, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(`{"all":true,"model_config_id":99999,"key":"invalid-profile-request"}`)))
	var rejected struct {
		Accepted []int64  `json:"accepted"`
		Runs     []int64  `json:"runs"`
		Errors   []string `json:"errors"`
	}
	if err = json.Unmarshal(invalid.Body.Bytes(), &rejected); err != nil {
		t.Fatal(err)
	}
	if invalid.Code != 200 || len(rejected.Accepted) != 0 || len(rejected.Runs) != 0 || len(rejected.Errors) == 0 {
		t.Fatal(invalid.Body.String())
	}
	w := httptest.NewRecorder()
	a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(`{"all":true,"model_config_id":2,"key":"all-failures-request"}`)))
	var result struct {
		Accepted []int64  `json:"accepted"`
		Runs     []int64  `json:"runs"`
		Errors   []string `json:"errors"`
		Skipped  int      `json:"skipped"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Accepted) != 206 || len(result.Runs) != 1 || len(result.Errors) != 1 || result.Skipped != 1 {
		t.Fatal(w.Body.String())
	}
	seen := map[int64]bool{}
	for _, id := range result.Runs {
		run, err := a.Jobs.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Targets) != 205 || run.ModelConfigID != alternative.ID {
			t.Fatal(run)
		}
		for _, target := range run.Targets {
			if seen[target] {
				t.Fatalf("file %d submitted twice", target)
			}
			seen[target] = true
		}
	}
	if len(seen) != 205 {
		t.Fatal(len(seen))
	}
	var count int
	if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='failure'").Scan(&count); err != nil || count != 208 {
		t.Fatal(count, err)
	}
	// Repeating the same request before completion reuses the queued runs.
	repeated := httptest.NewRecorder()
	a.retryHistory(repeated, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(`{"all":true,"model_config_id":2,"key":"all-failures-request"}`)))
	repeatedResult := result
	if err = json.Unmarshal(repeated.Body.Bytes(), &repeatedResult); err != nil {
		t.Fatal(err)
	}
	if repeated.Code != 200 || !slices.Equal(repeatedResult.Runs, result.Runs) || !slices.Equal(repeatedResult.Accepted, result.Accepted) || !slices.Equal(repeatedResult.Errors, result.Errors) || repeatedResult.Skipped != result.Skipped {
		t.Fatal(repeated.Body.String())
	}
	// A selection larger than 200 records also produces one batch.
	if _, err = a.DB.Exec("UPDATE runs SET status='cancelled' WHERE id=?", result.Runs[0]); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"ids": result.Accepted, "model_config_id": alternative.ID, "key": "selected-large-retry"})
	if err != nil {
		t.Fatal(err)
	}
	selection := httptest.NewRecorder()
	a.retryHistory(selection, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(string(payload))))
	if err = json.Unmarshal(selection.Body.Bytes(), &repeatedResult); err != nil {
		t.Fatal(err)
	}
	if len(repeatedResult.Accepted) != 206 || len(repeatedResult.Runs) != 1 || len(repeatedResult.Errors) != 0 {
		t.Fatal(selection.Body.String())
	}
	run, err := a.Jobs.Get(repeatedResult.Runs[0])
	if err != nil || len(run.Targets) != 205 || run.ModelConfigID != alternative.ID {
		t.Fatal(run, err)
	}
	// A single record can select another configuration, independent of its old run.
	if _, err = a.DB.Exec("UPDATE runs SET status='cancelled' WHERE id=?", run.ID); err != nil {
		t.Fatal(err)
	}
	single := httptest.NewRecorder()
	a.retryHistory(single, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(`{"ids":[1],"model_config_id":1,"key":"single-file-retry"}`)))
	if err = json.Unmarshal(single.Body.Bytes(), &repeatedResult); err != nil {
		t.Fatal(err)
	}
	if len(repeatedResult.Accepted) != 1 || len(repeatedResult.Runs) != 1 || len(repeatedResult.Errors) != 0 {
		t.Fatal(single.Body.String())
	}
	run, err = a.Jobs.Get(repeatedResult.Runs[0])
	if err != nil || len(run.Targets) != 1 || run.ModelConfigID != 1 {
		t.Fatal(run, err)
	}
}

func TestRetryAllHistoryEmptyAndInvalidScope(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"all":true,"key":"empty-retry-request"}`, 200},
		{`{"all":true,"ids":[1],"key":"invalid-retry-request"}`, 400},
		{`{"all":true,"key":"short"}`, 400},
		{`{"ids":[],"key":"empty-retry-request"}`, 400},
		{`{"all":true,"model_config_id":-1,"key":"invalid-model-request"}`, 400},
	} {
		w := httptest.NewRecorder()
		a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatal(tc.body, w.Code, w.Body.String())
		}
	}
}
