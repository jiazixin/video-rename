package model

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/prompt"
	"video-rename/internal/secret"
	"video-rename/internal/storage"
)

func TestModelReadsWhileWriterBusy(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "models.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	s := Store{db: db}
	if err := s.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	p, err := s.Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	p.BaseURL, p.Model, p.APIKey, p.Workers = "http://localhost/v1", "test-model", "test-secret", 4
	p, err = s.Save(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		settings := p.ServerConfig
		settings.Workers = 9
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), "UPDATE model_configs SET title='uncommitted model',settings=?,is_default=0 WHERE id=?", string(raw), p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), "UPDATE prompts SET title='uncommitted prompt',body='uncommitted body' WHERE id=?", p.PromptID); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			done <- func() error {
				got, err := s.Get(t.Context(), p.ID)
				if err != nil || !reflect.DeepEqual(got, p) {
					return fmt.Errorf("Get did not read committed profile: got=%+v err=%w", got, err)
				}
				profiles, err := s.List(t.Context())
				if err != nil || len(profiles) != 1 || !reflect.DeepEqual(profiles[0], p) {
					return fmt.Errorf("List did not read committed profiles: count=%d err=%w", len(profiles), err)
				}
				if id, err := s.DefaultID(t.Context()); err != nil || id != p.ID {
					return fmt.Errorf("DefaultID=%d err=%w, want %d", id, err, p.ID)
				}
				if workers, err := s.Workers(t.Context(), p.ID); err != nil || workers != p.Workers {
					return fmt.Errorf("Workers=%d err=%w, want %d", workers, err, p.Workers)
				}
				resolved := Profile{PromptID: p.PromptID}
				if err := s.resolve(t.Context(), &resolved); err != nil || resolved.PromptTitle != p.PromptTitle || resolved.Body != p.Body {
					return fmt.Errorf("resolve did not read committed prompt: title=%q body=%q err=%w", resolved.PromptTitle, resolved.Body, err)
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

		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(t.Context(), p.ID)
	if err != nil || got.Title != "uncommitted model" || got.Body != "uncommitted body" || got.Workers != 9 || got.Default {
		t.Fatalf("Get did not observe committed update: got=%+v err=%v", got, err)
	}
}

// newPrompt creates a library entry so profiles have a reference to carry.
type newPromptOptions struct {
	s     Store
	title string
	body  string
}

func newPrompt(t *testing.T, options newPromptOptions) prompt.Profile {
	t.Helper()
	s, title, body := options.s, options.title, options.body

	p, err := prompt.New(s.db).Save(t.Context(), prompt.Profile{Title: title, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultSaveConflictAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{db}
	if err = s.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	p, err := s.Get(t.Context(), 1)
	if err != nil || p.Body != DefaultBody {
		t.Fatal(p, err)
	}
	p.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", TimeoutMS: 2000, Temperature: 0.2, MaxTokens: 128}
	old := p
	lib := prompt.New(db)
	seedPrompt, err := lib.Get(t.Context(), p.PromptID)
	if err != nil {
		t.Fatal(err)
	}
	seedPrompt.Body = "页面更新要求"
	if _, err = lib.Save(t.Context(), seedPrompt); err != nil {
		t.Fatal(err)
	}
	p, err = s.Save(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(t.Context(), old); err == nil {
		t.Fatal("stale edit accepted")
	}
	if _, err = s.Save(t.Context(), Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", TimeoutMS: 2000, Temperature: 0.2, MaxTokens: 128, Title: " ", Body: "x"}); err == nil {
		t.Fatal("blank title accepted")
	}
	if _, err = s.Save(t.Context(), Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", TimeoutMS: 2000, Temperature: 0.2, MaxTokens: 128, Title: "x"}); err == nil {
		t.Fatal("missing prompt accepted")
	}
	second, err := s.Save(t.Context(), Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", TimeoutMS: 2000, Temperature: 0.2, MaxTokens: 128, Title: "旅行", PromptID: newPrompt(t, newPromptOptions{s: s, title: "风景提示词", body: "描述风景"}).ID})
	if err != nil || second.ID == 1 {
		t.Fatal(second, err)
	}
	if err = s.SetDefault(t.Context(), second.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetDefault(t.Context(), 99999); err == nil {
		t.Fatal("missing default accepted")
	}
	if err := db.Close(); err != nil {
		t.Error(err)
	}
	db, err = storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	s = Store{db}
	if err = s.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	p, err = s.Get(t.Context(), 1)
	if err != nil || p.Body != "页面更新要求" {
		t.Fatal(p, err)
	}
	if id, err := s.DefaultID(t.Context()); err != nil || id != second.ID {
		t.Fatal("default did not survive restart", id, err)
	}
	rows, err := s.List(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
}

func TestCredentialsHiddenEncryptedAndSnapshotIndependent(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	s := Store{db}
	settings := DefaultSettings()
	settings.BaseURL = "http://localhost:8080/v1"
	settings.Model = "vision-a"
	settings.APIKey = "private-key-only-for-test"
	lib := prompt.New(db)
	libraryPrompt, err := lib.Save(t.Context(), prompt.Profile{Title: "测试提示词", Body: "旧提示词"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(t.Context(), Profile{ServerConfig: settings, Title: "测试", PromptID: libraryPrompt.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), settings.APIKey.Reveal()) || strings.Contains(string(raw), `"api_key"`) {
		t.Fatal("key leaked in JSON")
	}
	var stored string
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT api_key FROM model_configs WHERE id=?", p.ID).Scan(&stored)
	}); err != nil {
		t.Error(err)
	}
	if stored == settings.APIKey.Reveal() || stored == "" {
		t.Fatal("plaintext key stored")
	}
	snapshot, err := s.Snapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	changed := p
	changed.BaseURL = "http://localhost:9999/v1"
	changed.APIKey = ""
	if s.ResolveKey(t.Context(), &changed, nil) == nil {
		t.Fatal("stored key forwarded to another URL")
	}
	empty := secret.Value("")
	if err = s.ResolveKey(t.Context(), &changed, &empty); err != nil || changed.APIKey != "" {
		t.Fatal("explicit key clear failed")
	}
	p.Body = "新提示词"
	p.Model = "vision-b"
	p.APIKey = "updated-key"
	if _, err = s.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	libraryPrompt.Body = "新提示词"
	if _, err = lib.Save(t.Context(), libraryPrompt); err != nil {
		t.Fatal(err)
	}
	var cfg config.TaskConfig
	if err = s.ApplySnapshot(snapshot, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Model != "vision-a" || cfg.Server.APIKey != settings.APIKey || cfg.Prompt != "旧提示词" {
		t.Fatal("snapshot mutated")
	}
	rows, err := s.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "updated-key") {
		t.Fatal("list leaked key")
	}
}

func TestCopyAndDelete(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	s := Store{db}
	if err = s.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	p, err := s.Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	p.ServerConfig = config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://localhost/v1", APIKey: "secret", Model: "test", TimeoutMS: 2000, MaxTokens: 1024, Workers: 4, Thinking: "disabled", ThinkingAdapter: "template"}
	p, err = s.Save(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := s.Duplicate(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copy.ID == p.ID || copy.Default || copy.APIKey != "secret" || copy.Workers != 4 || copy.Thinking != "disabled" || copy.Body != p.Body {
		t.Fatal("copy mismatch", copy.ID)
	}
	raw, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatal("key leaked")
	}
	if err = s.Delete(t.Context(), p.ID, p.Version); err == nil {
		t.Fatal("deleted default")
	}
	if err = s.Delete(t.Context(), copy.ID, copy.Version+1); err == nil {
		t.Fatal("stale delete")
	}
	_, err = storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next,model_config_id) VALUES('test','[1]',0,'daily','0 1 * * *','UTC',0,'',?)", copy.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), copy.ID, copy.Version); err == nil {
		t.Fatal("deleted bound profile")
	}
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DELETE FROM schedules") }); err != nil {
		t.Error(err)
	}
	if err = s.Delete(t.Context(), copy.ID, copy.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(t.Context(), copy.ID); err == nil {
		t.Fatal("still exists")
	}
}
