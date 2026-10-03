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
	"video-rename/internal/jobs"
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
	if strings.Contains(invalid.Body.String(), "sql:") || !strings.Contains(invalid.Body.String(), "模型配置不存在或已删除") {
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
		{`{"category":"success","all":true,"key":"invalid-success-all"}`, 400},
		{`{"category":"invalid","ids":[1],"key":"invalid-category-request"}`, 400},
	} {
		w := httptest.NewRecorder()
		a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatal(tc.body, w.Code, w.Body.String())
		}
	}
}

func TestRetryHistoryMissingFilesDoNotBlockValidTargets(t *testing.T) {
	for _, all := range []bool{true, false} {
		t.Run(fmt.Sprintf("all=%t", all), func(t *testing.T) {
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
			if err = os.Mkdir(filepath.Join(root, "parent"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"valid.mp4", "unindexed.mp4", "deleted.mp4", "parent/nested.mp4"} {
				if err = os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = a.Library.Refresh(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			var parent int64
			if err = a.DB.QueryRow("SELECT id FROM entries WHERE name='parent'").Scan(&parent); err != nil {
				t.Fatal(err)
			}
			if err = a.Library.Refresh(t.Context(), parent); err != nil {
				t.Fatal(err)
			}
			for i, name := range []string{"valid.mp4", "unindexed.mp4", "deleted.mp4", "nested.mp4", "valid.mp4"} {
				if _, err = a.DB.Exec("INSERT INTO events(id,category,action,entry,old_path,created) SELECT ?,'failure','automatic',id,name,'now' FROM entries WHERE name=?", i+1, name); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = a.DB.Exec("INSERT INTO events(id,category,action,entry,created) VALUES(6,'failure','automatic',99999,'now'),(7,'failure','manual',0,'now')"); err != nil {
				t.Fatal(err)
			}
			if _, err = a.DB.Exec("UPDATE entries SET present=0 WHERE name IN ('unindexed.mp4','parent')"); err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(filepath.Join(root, "deleted.mp4")); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"model_config_id": 1, "key": "mixed-files-retry"}
			if all {
				payload["all"] = true
			} else {
				payload["ids"] = []int64{1, 2, 3, 4, 5, 6, 7}
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(string(body))))
			var result struct {
				Accepted []int64  `json:"accepted"`
				Runs     []int64  `json:"runs"`
				Errors   []string `json:"errors"`
				Skipped  int      `json:"skipped"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(result.Accepted) != 2 || !slices.Contains(result.Accepted, 1) || !slices.Contains(result.Accepted, 5) || len(result.Runs) != 1 {
				t.Fatal(w.Body.String())
			}
			wantErrors, wantSkipped := 5, 0
			if all {
				wantErrors, wantSkipped = 4, 1
			}
			if len(result.Errors) != wantErrors || result.Skipped != wantSkipped || strings.Contains(w.Body.String(), "sql:") || strings.Contains(w.Body.String(), "no such file") {
				t.Fatal(w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "文件或上级目录已不在索引中") || !strings.Contains(w.Body.String(), "文件或上级目录不存在") {
				t.Fatal(w.Body.String())
			}
			run, err := a.Jobs.Get(result.Runs[0])
			if err != nil || len(run.Targets) != 1 {
				t.Fatal(run, err)
			}
			var count int
			if err = a.DB.QueryRow("SELECT count(*) FROM events WHERE category='failure'").Scan(&count); err != nil || count != 7 {
				t.Fatal(count, err)
			}
		})
	}
}

func setupSuccessfulHistoryRetry(t *testing.T) (*App, int64, int64, int64) {
	t.Helper()
	root := t.TempDir()
	a, err := New(&config.Config{Path: root, Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	tools := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	store := models.Store{DB: a.DB}
	profile, err := store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "retry-model", Timeout: "2s", MaxTokens: 128}
	if _, err := store.Save(profile); err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds, settings.RetryEnabled = 0, false
	if err := (systemsettings.Store{DB: a.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "original.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var entry int64
	if err := a.DB.QueryRow("SELECT id FROM entries WHERE name='original.mp4'").Scan(&entry); err != nil {
		t.Fatal(err)
	}
	snap, err := domain.StatSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	op, err := a.Library.AutoCommit(entry, snap, "first-title", "initial-auto-name", 0)
	if err != nil {
		t.Fatal(err)
	}
	var event int64
	if err := a.DB.QueryRow("SELECT id FROM events WHERE operation=?", op.ID).Scan(&event); err != nil {
		t.Fatal(err)
	}
	return a, entry, event, op.ID
}

func TestRetrySuccessHistoryPreservesProtection(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancel", "marked-during-inference"} {
		t.Run(outcome, func(t *testing.T) {
			a, entry, event, operation := setupSuccessfulHistoryRetry(t)
			body := fmt.Sprintf(`{"category":"success","ids":[%d,%d],"model_config_id":1,"key":"success-history-retry"}`, event, event)
			w := httptest.NewRecorder()
			a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(body)))
			var result struct {
				Accepted []int64  `json:"accepted"`
				Runs     []int64  `json:"runs"`
				Errors   []string `json:"errors"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Runs) != 1 || len(result.Accepted) != 2 || len(result.Errors) != 0 {
				t.Fatal(w.Body.String(), err)
			}
			// Submitting a retry never removes the mark, and repeating it while
			// queued reuses the batch rather than granting a second permission.
			e, err := a.Library.Get(entry)
			if err != nil || e.Processed != "automatic" || e.RestoreID != operation {
				t.Fatal(e, err)
			}
			repeated := httptest.NewRecorder()
			a.retryHistory(repeated, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(body)))
			var repeat struct {
				Runs []int64 `json:"runs"`
			}
			if err := json.Unmarshal(repeated.Body.Bytes(), &repeat); err != nil || !slices.Equal(repeat.Runs, result.Runs) {
				t.Fatal(repeated.Body.String(), err)
			}
			var frozen string
			if err := a.DB.QueryRow("SELECT retry_snapshot FROM runs WHERE id=?", result.Runs[0]).Scan(&frozen); err != nil {
				t.Fatal(err)
			}
			var policy struct{ RetryOperations map[int64]int64 }
			if err := json.Unmarshal([]byte(frozen), &policy); err != nil || policy.RetryOperations[entry] != operation {
				t.Fatal(frozen, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			a.Jobs.Analyze = func(_ context.Context, cfg *config.Config, _ domain.VideoJob, _, _ string) (string, error) {
				calls++
				if cfg.Server.Model != "retry-model" {
					t.Errorf("wrong retry model: %s", cfg.Server.Model)
				}
				switch outcome {
				case "failure":
					return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "invalid title"}
				case "cancel":
					cancel()
					return "", ctx.Err()
				case "marked-during-inference":
					// Simulate protection changing after the initial worker check.
					if _, err := a.DB.Exec("UPDATE marks SET source='marked',operation=NULL"); err != nil {
						t.Error(err)
					}
				}
				return "second-title", nil
			}
			a.Jobs.Execute(ctx, result.Runs[0])
			run, err := a.Jobs.Get(result.Runs[0])
			if err != nil || calls != 1 || (run.Done == 1) != (outcome == "success") {
				t.Fatal(run, calls, err)
			}
			e, err = a.Library.Get(entry)
			if err != nil || e.Processed == "" {
				t.Fatal("protection was removed", e, err)
			}
			if outcome == "success" {
				if e.Name != "second-title.mp4" || e.RestoreID == operation || e.Processed != "automatic" {
					t.Fatal(e)
				}
				if err := a.Library.CheckAutomaticRetry(entry, operation); err == nil {
					t.Fatal("superseded record can still authorize a retry")
				}
			} else if e.Name != "first-title.mp4" || e.RestoreID != operation {
				t.Fatal("unsuccessful retry changed the file", e)
			}
			var originals int
			if err := a.DB.QueryRow("SELECT count(*) FROM events WHERE id=?", event).Scan(&originals); err != nil || originals != 1 {
				t.Fatal("original success record was removed", originals, err)
			}
			ordinary, err := a.Jobs.Create(jobs.Request{Targets: []int64{entry}, ModelConfigID: 1, Key: "ordinary-after-retry"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			a.Jobs.Execute(t.Context(), ordinary)
			run, err = a.Jobs.Get(ordinary)
			if err != nil || run.Skipped != 1 || calls != 1 {
				t.Fatal("ordinary batch bypassed protection", run, calls, err)
			}
		})
	}
}

func TestRetrySuccessHistoryRejectsStaleAndProtectedRecords(t *testing.T) {
	for _, change := range []string{"marked", "manual", "restored", "content-changed"} {
		t.Run(change, func(t *testing.T) {
			a, entry, event, operation := setupSuccessfulHistoryRetry(t)
			e, err := a.Library.Get(entry)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "marked":
				if result := a.Library.SetProcessed(t.Context(), []int64{entry}, false); result.Failed > 0 {
					t.Fatal(result)
				}
				if result := a.Library.SetProcessed(t.Context(), []int64{entry}, true); result.Failed > 0 {
					t.Fatal(result)
				}
			case "manual":
				if _, err := a.Library.Rename(entry, e.Version, "manual-title.mp4", "manual-after-success"); err != nil {
					t.Fatal(err)
				}
			case "restored":
				if _, err := a.Library.Restore(operation, "restore-before-retry"); err != nil {
					t.Fatal(err)
				}
			case "content-changed":
				if err := os.WriteFile(filepath.Join(a.Library.Root, e.Name), []byte("changed video content"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := a.Library.Refresh(t.Context(), 1); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			body := fmt.Sprintf(`{"category":"success","ids":[%d],"key":"protected-success-retry"}`, event)
			a.retryHistory(w, httptest.NewRequest("POST", "/api/logs/retry", strings.NewReader(body)))
			var result struct {
				Runs   []int64  `json:"runs"`
				Errors []string `json:"errors"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Runs) != 0 || len(result.Errors) != 1 {
				t.Fatal(w.Body.String(), err)
			}
		})
	}
}

func TestRetrySuccessPermissionCannotApplyToGenericRuns(t *testing.T) {
	for _, field := range []string{"RetryOperations", "retry_operations"} {
		w := httptest.NewRecorder()
		body := fmt.Sprintf(`{"targets":[1],"key":"generic-run-request","%s":{"1":1}}`, field)
		var input jobs.Request
		if decode(w, httptest.NewRequest("POST", "/api/runs", strings.NewReader(body)), &input) || w.Code != 400 {
			t.Fatal("generic run JSON accepted retry permission", w.Code, input)
		}
	}
	a, entry, _, operation := setupSuccessfulHistoryRetry(t)
	for _, tc := range []struct {
		target    int64
		recursive bool
		source    string
		plan      int64
	}{
		{entry, true, "manual", 0},
		{entry, false, "scheduled", 0},
		{entry, false, "manual", 1},
		{1, false, "manual", 0},
	} {
		_, err := a.Jobs.Create(jobs.Request{Targets: []int64{tc.target}, Recursive: tc.recursive, Key: "restricted-retry-scope", RetryOperations: map[int64]int64{tc.target: operation}}, tc.source, tc.plan)
		if err == nil {
			t.Fatalf("retry permission escaped selected-file scope: %+v", tc)
		}
	}
}
