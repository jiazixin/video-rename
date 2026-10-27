package jobs

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/pipeline"
	"video-rename/internal/systemsettings"
)

func TestFailureKeywordSnapshotPrimaryRetryAndFallback(t *testing.T) {
	m, root := setup(t)
	keywords := []string{"Cannot see this scene"}
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		prompt := body["messages"].([]any)[0].(map[string]any)["content"].(string)
		if strings.Contains(strings.ToLower(prompt), strings.ToLower(keywords[0])) {
			t.Error("failure rule was sent to the model", prompt)
		}
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		fmt.Fprint(w, `{"choices":[{"message":{"content":"I CANNOT SEE THIS SCENE, sorry."}}]}`)
	}))
	defer server.Close()
	modelsStore := models.Store{DB: m.DB}
	primary, err := modelsStore.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	primary.BaseURL = server.URL + "/primary"
	primary, err = modelsStore.Save(primary)
	if err != nil {
		t.Fatal(err)
	}
	fallback := primary
	fallback.ID, fallback.Title, fallback.BaseURL = 0, "冻结规则备用模型", server.URL+"/fallback"
	fallback, err = modelsStore.Save(fallback)
	if err != nil {
		t.Fatal(err)
	}
	settingsStore := systemsettings.Store{DB: m.DB}
	settings := systemsettings.Default()
	settings.StabilitySeconds, settings.RetryCount, settings.FallbackModelID = 0, 2, fallback.ID
	if err = settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	if _, err = settingsStore.SaveFailureKeywords(keywords); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "original.mp4")
	if err = os.WriteFile(original, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Prepare = func(_ context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		return fakeFrames(work)
	}
	m.Infer = func(ctx context.Context, cfg *config.TaskConfig, frames []domain.Frame) (string, error) {
		if !slices.Equal(cfg.FailureKeywords, keywords) {
			t.Errorf("attempt changed frozen rules: %v", cfg.FailureKeywords)
		}
		return pipeline.Infer(ctx, cfg, frames)
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "keyword-fallback-snapshot"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Editing settings after queueing cannot change any attempt in this batch.
	if _, err = settingsStore.SaveFailureKeywords([]string{}); err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	run, err := m.Get(t.Context(), id)
	if err != nil || run.Failed != 1 || run.Done != 0 || run.Status != "failed" {
		t.Fatal(run, err)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatal("failure response renamed original file", err)
	}
	mu.Lock()
	actual := slices.Clone(calls)
	mu.Unlock()
	if !slices.Equal(actual, []string{"/primary/chat/completions", "/primary/chat/completions", "/fallback/chat/completions"}) {
		t.Fatal("wrong retry/fallback attempts", actual)
	}
	var itemID int64
	if err := m.DB.QueryRow("SELECT id FROM run_items WHERE run=?", id).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	attempts, err := Attempts(t.Context(), m.DB, []int64{itemID})
	if err != nil || len(attempts[itemID]) != 3 || !attempts[itemID][2].Fallback {
		t.Fatal(attempts, err)
	}
	for _, attempt := range attempts[itemID] {
		if attempt.Status != "failed" || !strings.Contains(attempt.Error.Message, keywords[0]) {
			t.Fatal("attempt did not use custom phrase", attempt)
		}
	}
	var message, response string
	if err := m.DB.QueryRow("SELECT message,raw_response FROM events WHERE run=? AND category='failure'", id).Scan(&message, &response); err != nil || !strings.Contains(message, `"code":"naming_failed"`) || !strings.Contains(response, "I CANNOT SEE THIS SCENE") {
		t.Fatal("missing naming failure diagnostics", message, response, err)
	}
}

func TestDeletedFailureKeywordsStayDeletedInQueuedBatch(t *testing.T) {
	for _, rules := range [][]string{{}, {"自定义失败词句"}} {
		t.Run(fmt.Sprint(rules), func(t *testing.T) {
			m, root := setup(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"命名失败"}}]}`)
			}))
			defer server.Close()
			modelsStore := models.Store{DB: m.DB}
			primary, err := modelsStore.Get(1)
			if err != nil {
				t.Fatal(err)
			}
			primary.BaseURL = server.URL
			if _, err := modelsStore.Save(primary); err != nil {
				t.Fatal(err)
			}
			settingsStore := systemsettings.Store{DB: m.DB}
			if _, err := settingsStore.SaveFailureKeywords(rules); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "original.mp4"), []byte("video"), 0o600); err != nil {
				t.Fatal(err)
			}
			m.Prepare = func(_ context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
				return fakeFrames(work)
			}
			m.Infer = pipeline.Infer
			id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "deleted-rule-snapshot"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := settingsStore.SaveFailureKeywords(systemsettings.DefaultFailureKeywords()); err != nil {
				t.Fatal(err)
			}
			m.Execute(t.Context(), id)
			run, err := m.Get(t.Context(), id)
			if err != nil || run.Done != 1 || run.Failed != 0 {
				t.Fatal("deleted default marker or empty list was reinstated", run, err)
			}
			if _, err := os.Stat(filepath.Join(root, "命名失败.mp4")); err != nil {
				t.Fatal("title still blocked after marker deletion", err)
			}
		})
	}
}
