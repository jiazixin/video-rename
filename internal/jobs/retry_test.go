package jobs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/pipeline"
	"video-rename/internal/systemsettings"
)

func TestRetryFallbackSnapshotAndActualRequests(t *testing.T) {
	m, root := setup(t)
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		model := "primary"
		if r.URL.Path == "/fallback/chat/completions" {
			model = "fallback"
		}
		calls = append(calls, model)
		w.Header().Set("Content-Type", "application/json")
		if model == "primary" {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"命名失败"}}],"usage":{"total_tokens":10}}`)
		} else {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"兜底生成的新标题"}}],"usage":{"total_tokens":20}}`)
		}
	}))
	defer server.Close()
	store := models.Store{DB: m.DB}
	primary, err := store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	primary.BaseURL = server.URL + "/primary"
	if _, err = store.Save(primary); err != nil {
		t.Fatal(err)
	}
	fallback := primary
	fallback.ID = 0
	fallback.Title = "备用配置"
	fallback.Model = "fallback-model"
	fallback.BaseURL = server.URL + "/fallback"
	fallback, err = store.Save(fallback)
	if err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	settings.FallbackModelID = fallback.ID
	if err = (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Prepare = func(_ context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		return fakeFrames(work)
	}
	m.Infer = pipeline.Infer
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Recursive: true, Key: "retry-snapshot"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Later edits cannot change a queued batch's retry rules, destination or prompt.
	settings.RetryEnabled = false
	settings.FallbackModelID = 0
	if err = (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	fallback.BaseURL = "http://127.0.0.1:1"
	if _, err = store.Save(fallback); err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	run, err := m.Get(t.Context(), id)
	if err != nil || run.Done != 1 || run.TokenCount != 50 || run.TokenRecords != 4 {
		t.Fatal(run, err)
	}
	mu.Lock()
	actual := slices.Clone(calls)
	mu.Unlock()
	if !slices.Equal(actual, []string{"primary", "primary", "primary", "fallback"}) {
		t.Fatal(actual)
	}
	var itemID int64
	if err = m.DB.QueryRow("SELECT id FROM run_items WHERE run=?", id).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	byItem, err := Attempts(t.Context(), m.DB, []int64{itemID})
	attempts := byItem[itemID]
	if err != nil || len(attempts) != 4 || !attempts[3].Fallback || attempts[3].Status != "success" {
		t.Fatal(attempts, err)
	}
	var tokens int
	if err = m.DB.QueryRow("SELECT sum(tokens) FROM token_usage_records WHERE model_config_id=?", fallback.ID).Scan(&tokens); err != nil || tokens != 20 {
		t.Fatal(tokens, err)
	}
}

func TestPermanentErrorGoesDirectlyToFallback(t *testing.T) {
	m, root := setup(t)
	store := models.Store{DB: m.DB}
	profile, err := store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.ID = 0
	profile.Title = "fallback"
	profile.Model = "fallback"
	profile, err = store.Save(profile)
	if err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	settings.FallbackModelID = profile.ID
	if err = (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	m.Analyze = func(_ context.Context, cfg *config.TaskConfig, _ domain.VideoJob, _, _ string) (string, error) {
		calls = append(calls, cfg.Server.Model)
		if cfg.Server.Model == "fallback" {
			return "成功", nil
		}
		return "", &domain.JobError{Code: domain.CodeAPIUnauthorized, Global: true, HTTPStatus: 401}
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "direct-fallback"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	run, _ := m.Get(t.Context(), id)
	if run.Done != 1 || !slices.Equal(calls, []string{"test-model", "fallback"}) {
		t.Fatal(run, calls)
	}
}

func TestRetryClassificationAndCancellation(t *testing.T) {
	for _, code := range []domain.ErrorCode{domain.CodeNamingFailed, domain.CodeTitleInvalid, domain.CodeAPITimeout, domain.CodeAPIRateLimited} {
		again, _, _ := retryKind(&domain.JobError{Code: code})
		if !again {
			t.Fatal(code)
		}
	}
	for _, code := range []domain.ErrorCode{domain.CodeCancelled, domain.CodeSourceChanged, domain.CodeExtractFailed, domain.CodeRenamePermissionDenied} {
		again, fallback, _ := retryKind(&domain.JobError{Code: code})
		if again || fallback {
			t.Fatal(code)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	if err := retryWait(ctx, time.Hour); err == nil || time.Since(start) > time.Second {
		t.Fatal(err)
	}
}

func TestRetainedFramesSurviveRenameRestoreAndModelChange(t *testing.T) {
	m, root := setup(t)
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	settings.SuccessCacheMode = "keep"
	if err := m.Cache.Configure(settings.Bytes(), settings.Policy); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	extracted := 0
	m.Prepare = func(_ context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		extracted++
		return fakeFrames(work)
	}
	m.Infer = func(context.Context, *config.TaskConfig, []domain.Frame) (string, error) {
		return "第一次标题", nil
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "retain-first"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	result, err := m.Library.RestoreBatch(t.Context(), id)
	if err != nil || result.Restored != 1 {
		t.Fatal(result, err)
	}
	profile, err := (models.Store{DB: m.DB}).Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.Model = "another-model"
	if _, err = (models.Store{DB: m.DB}).Save(profile); err != nil {
		t.Fatal(err)
	}
	m.Infer = func(context.Context, *config.TaskConfig, []domain.Frame) (string, error) {
		return "第二次标题", nil
	}
	id, err = m.Create(t.Context(), Request{Targets: []int64{1}, Key: "retain-second"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	run, _ := m.Get(t.Context(), id)
	if run.Done != 1 || extracted != 1 || m.Cache.Stats().Entries != 1 {
		t.Fatal(run, extracted, m.Cache.Stats())
	}
}

func TestRetryDisabledExhaustionAndCancel(t *testing.T) {
	for _, mode := range []string{"disabled", "exhausted", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			m, root := setup(t)
			if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
				t.Fatal(err)
			}
			settings := systemsettings.Default()
			settings.StabilitySeconds = 0
			settings.RetryCount = 1
			settings.RetryEnabled = mode != "disabled"
			if err := (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			m.Analyze = func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error) {
				calls++
				if mode == "cancelled" {
					cancel()
				}
				return "", &domain.JobError{Code: domain.CodeNamingFailed, Message: "命名失败"}
			}
			id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "retry-" + mode}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			m.Execute(ctx, id)
			want := 1
			if mode == "exhausted" {
				want = 2
			}
			if calls != want {
				t.Fatal(mode, calls)
			}
			run, _ := m.Get(t.Context(), id)
			if mode != "cancelled" && run.Failed != 1 {
				t.Fatal(run)
			}
		})
	}
}

func TestUnusableFilenameRetriesBeforeCommit(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	settings.RetryCount = 1
	if err := (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.Analyze = func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error) {
		calls++
		if calls == 1 {
			return "...", nil
		}
		return "有效标题", nil
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "invalid-filename"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	run, _ := m.Get(t.Context(), id)
	if calls != 2 || run.Done != 1 {
		t.Fatal(calls, run)
	}
}
