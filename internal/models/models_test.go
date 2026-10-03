package models

import (
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"video-rename/internal/config"
	"video-rename/internal/prompts"
	"video-rename/internal/storage"
)

func TestModelReadsWhileWriterBusy(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "models.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{DB: db}
	if err := s.Seed(); err != nil {
		t.Fatal(err)
	}
	p, err := s.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	p.BaseURL, p.Model, p.APIKey, p.Workers = "http://localhost/v1", "test-model", "test-secret", 4
	p, err = s.Save(p)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	settings := p.ServerConfig
	settings.Workers = 9
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE model_configs SET title='uncommitted model',settings=?,is_default=0 WHERE id=?", string(raw), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE prompts SET title='uncommitted prompt',body='uncommitted body' WHERE id=?", p.PromptID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- func() error {
			got, err := s.Get(p.ID)
			if err != nil || !reflect.DeepEqual(got, p) {
				return fmt.Errorf("Get did not read committed profile: got=%+v err=%v", got, err)
			}
			profiles, err := s.List()
			if err != nil || len(profiles) != 1 || !reflect.DeepEqual(profiles[0], p) {
				return fmt.Errorf("List did not read committed profiles: count=%d err=%v", len(profiles), err)
			}
			if id, err := s.DefaultID(); err != nil || id != p.ID {
				return fmt.Errorf("DefaultID=%d err=%v, want %d", id, err, p.ID)
			}
			if workers, err := s.Workers(t.Context(), p.ID); err != nil || workers != p.Workers {
				return fmt.Errorf("Workers=%d err=%v, want %d", workers, err, p.Workers)
			}
			resolved := Profile{PromptID: p.PromptID}
			if err := s.resolve(&resolved); err != nil || resolved.PromptTitle != p.PromptTitle || resolved.Body != p.Body {
				return fmt.Errorf("resolve did not read committed prompt: title=%q body=%q err=%v", resolved.PromptTitle, resolved.Body, err)
			}
			return nil
		}()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("model reads waited for the writer")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(p.ID)
	if err != nil || got.Title != "uncommitted model" || got.Body != "uncommitted body" || got.Workers != 9 || got.Default {
		t.Fatalf("Get did not observe committed update: got=%+v err=%v", got, err)
	}
}

// newPrompt creates a library entry so profiles have a reference to carry.
func newPrompt(t *testing.T, s Store, title, body string) prompts.Profile {
	t.Helper()
	p, err := prompts.Store{DB: s.DB}.Save(prompts.Profile{Title: title, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultSaveConflictAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{db}
	if err = s.Seed(); err != nil {
		t.Fatal(err)
	}
	p, err := s.Get(1)
	if err != nil || p.Body != DefaultBody {
		t.Fatal(p, err)
	}
	p.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128}
	old := p
	lib := prompts.Store{DB: db}
	seedPrompt, err := lib.Get(p.PromptID)
	if err != nil {
		t.Fatal(err)
	}
	seedPrompt.Body = "页面更新要求"
	if _, err = lib.Save(seedPrompt); err != nil {
		t.Fatal(err)
	}
	p, err = s.Save(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(old); err == nil {
		t.Fatal("stale edit accepted")
	}
	if _, err = s.Save(Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128, Title: " ", Body: "x"}); err == nil {
		t.Fatal("blank title accepted")
	}
	if _, err = s.Save(Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128, Title: "x"}); err == nil {
		t.Fatal("missing prompt accepted")
	}
	second, err := s.Save(Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128, Title: "旅行", PromptID: newPrompt(t, s, "风景提示词", "描述风景").ID})
	if err != nil || second.ID == 1 {
		t.Fatal(second, err)
	}
	if err = s.SetDefault(second.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetDefault(99999); err == nil {
		t.Fatal("missing default accepted")
	}
	db.Close()
	db, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = Store{db}
	if err = s.Seed(); err != nil {
		t.Fatal(err)
	}
	p, err = s.Get(1)
	if err != nil || p.Body != "页面更新要求" {
		t.Fatal(p, err)
	}
	if id, err := s.DefaultID(); err != nil || id != second.ID {
		t.Fatal("default did not survive restart", id, err)
	}
	rows, err := s.List()
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
}

func TestCredentialsHiddenEncryptedAndSnapshotIndependent(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{db}
	settings := DefaultSettings()
	settings.BaseURL = "http://localhost:8080/v1"
	settings.Model = "vision-a"
	settings.APIKey = "private-key-only-for-test"
	lib := prompts.Store{DB: db}
	libraryPrompt, err := lib.Save(prompts.Profile{Title: "测试提示词", Body: "旧提示词"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{ServerConfig: settings, Title: "测试", PromptID: libraryPrompt.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), settings.APIKey) || strings.Contains(string(raw), `"api_key"`) {
		t.Fatal("key leaked in JSON")
	}
	var stored string
	db.QueryRow("SELECT api_key FROM model_configs WHERE id=?", p.ID).Scan(&stored)
	if stored == settings.APIKey || stored == "" {
		t.Fatal("plaintext key stored")
	}
	snapshot, err := s.Snapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	changed := p
	changed.BaseURL = "http://localhost:9999/v1"
	changed.APIKey = ""
	if s.ResolveKey(&changed, nil) == nil {
		t.Fatal("stored key forwarded to another URL")
	}
	empty := ""
	if err = s.ResolveKey(&changed, &empty); err != nil || changed.APIKey != "" {
		t.Fatal("explicit key clear failed")
	}
	p.Body = "新提示词"
	p.Model = "vision-b"
	p.APIKey = "updated-key"
	if _, err = s.Save(p); err != nil {
		t.Fatal(err)
	}
	libraryPrompt.Body = "新提示词"
	if _, err = lib.Save(libraryPrompt); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err = s.ApplySnapshot(snapshot, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Model != "vision-a" || cfg.Server.APIKey != settings.APIKey || cfg.Prompt != "旧提示词" {
		t.Fatal("snapshot mutated")
	}
	rows, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(rows)
	if strings.Contains(string(raw), "updated-key") {
		t.Fatal("list leaked key")
	}
}

func TestCopyAndDelete(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{db}
	if err = s.Seed(); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get(1)
	p.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://localhost/v1", APIKey: "secret", Model: "test", Timeout: "2s", MaxTokens: 1024, Workers: 4, Thinking: "disabled", ThinkingAdapter: "template"}
	p, err = s.Save(p)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := s.Duplicate(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copy.ID == p.ID || copy.Default || copy.APIKey != "secret" || copy.Workers != 4 || copy.Thinking != "disabled" || copy.Body != p.Body {
		t.Fatal("copy mismatch", copy.ID)
	}
	raw, _ := json.Marshal(copy)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("key leaked")
	}
	if err = s.Delete(p.ID, p.Version); err == nil {
		t.Fatal("deleted default")
	}
	if err = s.Delete(copy.ID, copy.Version+1); err == nil {
		t.Fatal("stale delete")
	}
	_, err = db.Exec("INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next,model_config_id) VALUES('test','[1]',0,'daily','0 1 * * *','UTC',0,'',?)", copy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(copy.ID, copy.Version); err == nil {
		t.Fatal("deleted bound profile")
	}
	db.Exec("DELETE FROM schedules")
	if err = s.Delete(copy.ID, copy.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(copy.ID); err == nil {
		t.Fatal("still exists")
	}
}
