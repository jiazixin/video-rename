package systemsetting

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestSettingsReadsRemainAvailableDuringWrite(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	store := Store{db: db}
	settings := Default()
	settings.ExtractWorkers = 3
	if err := store.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	numbering := domain.DefaultNumberingSettings()
	if err := store.SaveNumbering(t.Context(), numbering); err != nil {
		t.Fatal(err)
	}
	const prompt = "只输出已提交的标题。"
	if _, err := store.SaveFixedPrompt(t.Context(), prompt); err != nil {
		t.Fatal(err)
	}
	keywords := []string{"只读取已提交规则"}
	if _, err := store.SaveFailureKeywords(t.Context(), keywords); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("test rollback")
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "UPDATE system_settings SET extract_workers=17,numbering='{',fixed_prompt='未提交',policy='{'"); err != nil {
			t.Fatal(err)
		}
		for _, check := range []struct {
			name string
			read func() error
		}{
			{"processing", func() error {
				value, err := store.Get(t.Context())
				if err == nil && value != settings {
					err = fmt.Errorf("settings: got %+v, want %+v", value, settings)
				}
				return err
			}},
			{"numbering", func() error {
				value, err := store.Numbering(t.Context())
				if err == nil && value != numbering {
					err = fmt.Errorf("numbering: got %+v, want %+v", value, numbering)
				}
				return err
			}},
			{"fixed prompt", func() error {
				value, err := store.FixedPrompt(t.Context())
				if err == nil && value != prompt {
					err = fmt.Errorf("prompt: got %q, want %q", value, prompt)
				}
				return err
			}},
			{"failure keywords", func() error {
				value, err := store.FailureKeywords(t.Context())
				if err == nil && !slices.Equal(value, keywords) {
					err = fmt.Errorf("keywords: got %q, want %q", value, keywords)
				}
				return err
			}},
		} {
			done := make(chan error, 1)
			go func() { done <- check.read() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("%s: %v", check.name, err)
				}
			case <-time.After(time.Second):
				<-done
				t.Fatalf("%s read waited for the writer connection", check.name)
			}
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}

func TestStabilityDefaultsValidationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.sqlite")
	db, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{db: db}
	value, err := s.Get(t.Context())
	if err != nil || value.StabilitySeconds != 30 {
		t.Fatal(value, err)
	}
	for _, seconds := range []int{-1, 3601} {
		value.StabilitySeconds = seconds
		if err = s.Save(t.Context(), value); err == nil {
			t.Fatal("invalid wait accepted", seconds)
		}
	}
	value.StabilitySeconds = 0
	if err = s.Save(t.Context(), value); err != nil {
		t.Fatal(err)
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
	value, err = (Store{db: db}).Get(t.Context())
	if err != nil || value.StabilitySeconds != 0 {
		t.Fatal(value, err)
	}
	value.StabilitySeconds = 3600
	if err = (Store{db: db}).Save(t.Context(), value); err != nil {
		t.Fatal(err)
	}
}

func TestFixedPromptDefaultsValidationAndRestart(t *testing.T) {
	if !strings.Contains(DefaultFixedPrompt, "只回复“命名失败”四个字") || !strings.Contains(DefaultFixedPrompt, "此时不适用上述标题字数要求") {
		t.Fatal("default fixed prompt lacks the naming-failure protocol or its length exception")
	}
	path := filepath.Join(t.TempDir(), "fixed.sqlite")
	db, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{db: db}
	fixed, err := s.FixedPrompt(t.Context())
	if err != nil || fixed != DefaultFixedPrompt {
		t.Fatal(fixed, err)
	}
	if _, err = s.SaveFixedPrompt(t.Context(), "  "); err == nil {
		t.Fatal("blank fixed prompt accepted")
	}
	if _, err = s.SaveFixedPrompt(t.Context(), strings.Repeat("字", 16001)); err == nil {
		t.Fatal("oversized fixed prompt accepted")
	}
	saved, err := s.SaveFixedPrompt(t.Context(), "只输出标题。")
	if err != nil || saved != "只输出标题。" {
		t.Fatal(saved, err)
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
	if fixed, err = (Store{db: db}).FixedPrompt(t.Context()); err != nil || fixed != "只输出标题。" {
		t.Fatal(fixed, err)
	}
}

func TestSettingsDoNotHideStorageErrors(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s := Store{db: db}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Numbering(t.Context()); err == nil {
		t.Fatal("closed database returned default numbering")
	}
	if _, err := s.FixedPrompt(t.Context()); err == nil {
		t.Fatal("closed database returned default prompt")
	}
}

func TestInvalidSavedNumberingFails(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	s := Store{db: db}
	if err = s.Save(t.Context(), Default()); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"{", `{"image":{"padding":-1}}`} {
		if _, err = storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), "UPDATE system_settings SET numbering=?", raw)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Numbering(t.Context()); err == nil {
			t.Fatalf("invalid rules accepted: %s", raw)
		}
	}
}

func TestRetryAndRetentionSettings(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	store := Store{db: db}
	value, err := store.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !value.RetryEnabled || value.RetryCount != 3 || value.SuccessCacheMode != "immediate" || value.IdleCacheHours != 72 {
		t.Fatal(value)
	}
	value.RetryCount = 5
	value.SuccessCacheMode = "delayed"
	value.SuccessCacheHours = 168
	value.IdleCacheHours = 0
	if err = store.Save(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context())
	if err != nil || got != value {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*Settings){func(s *Settings) { s.RetryCount = 11 }, func(s *Settings) { s.RetryCount = 0 }, func(s *Settings) { s.SuccessCacheHours = 0 }, func(s *Settings) { s.IdleCacheHours = -1 }, func(s *Settings) { s.SuccessCacheMode = "invalid" }} {
		invalid := value
		mutate(&invalid)
		if err = store.Save(t.Context(), invalid); err == nil {
			t.Fatal("accepted", invalid)
		}
	}
}

func TestNumberingDefaultsAndSavedChoice(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "numbering.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	store := Store{db: db}
	rules, err := store.Numbering(t.Context())
	if err != nil || !rules.Image.Compact || !rules.Audio.Compact {
		t.Fatal(rules, err)
	}
	rules.Image.Compact = false
	rules.Audio.Compact = false
	if err := store.SaveNumbering(t.Context(), rules); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Numbering(t.Context())
	if err != nil || saved != rules {
		t.Fatal(saved, err)
	}
}

func TestBatchLimitsDefaultsValidationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "limits.sqlite")
	db, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	store := Store{db: db}
	limits, err := store.BatchLimits(t.Context())
	if err != nil || limits.RunningBatchLimit != 32 || limits.WaitingBatchLimit != 1000 {
		t.Fatal(limits, err)
	}
	value := Default()
	if err := store.Save(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	// Existing policy documents without the optional fields use new defaults.
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE system_settings SET policy='{"retry_enabled":true,"retry_count":5}'`)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context())
	if err != nil || got.BatchLimits != limits || got.RetryCount != 5 {
		t.Fatal(got, err)
	}
	for _, pair := range [][2]int{{0, 1000}, {257, 1000}, {32, 0}, {32, 100001}} {
		value.RunningBatchLimit, value.WaitingBatchLimit = pair[0], pair[1]
		if err := store.Save(t.Context(), value); err == nil {
			t.Fatal("invalid capacity accepted", pair)
		}
	}
	for _, pair := range [][2]int{{1, 1}, {256, 100000}, {64, 5000}} {
		value.RunningBatchLimit, value.WaitingBatchLimit = pair[0], pair[1]
		if err := store.Save(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	store = Store{db: db}
	got, err = store.Get(t.Context())
	if err != nil || got != value {
		t.Fatal(got, err)
	}
	limits, err = store.BatchLimits(t.Context())
	if err != nil || limits != value.BatchLimits {
		t.Fatal(limits, err)
	}
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE system_settings SET policy='{"running_batch_limit":0,"waiting_batch_limit":5000}'`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BatchLimits(t.Context()); err == nil {
		t.Fatal("invalid stored capacity used")
	}
	if _, err := store.Get(t.Context()); err == nil {
		t.Fatal("invalid stored capacity shown as valid")
	}
}
