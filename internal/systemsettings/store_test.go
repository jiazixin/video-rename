package systemsettings

import (
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
	db, err := storage.Open(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Store{DB: db}
	settings := Default()
	settings.ExtractWorkers = 3
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	numbering := domain.DefaultNumberingSettings()
	if err := store.SaveNumbering(numbering); err != nil {
		t.Fatal(err)
	}
	const prompt = "只输出已提交的标题。"
	if _, err := store.SaveFixedPrompt(prompt); err != nil {
		t.Fatal(err)
	}
	keywords := []string{"只读取已提交规则"}
	if _, err := store.SaveFailureKeywords(keywords); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE system_settings SET extract_workers=17,numbering='{',fixed_prompt='未提交',policy='{'"); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name string
		read func() error
	}{
		{"processing", func() error {
			value, err := store.Get()
			if err == nil && value != settings {
				err = fmt.Errorf("settings: got %+v, want %+v", value, settings)
			}
			return err
		}},
		{"numbering", func() error {
			value, err := store.Numbering()
			if err == nil && value != numbering {
				err = fmt.Errorf("numbering: got %+v, want %+v", value, numbering)
			}
			return err
		}},
		{"fixed prompt", func() error {
			value, err := store.FixedPrompt()
			if err == nil && value != prompt {
				err = fmt.Errorf("prompt: got %q, want %q", value, prompt)
			}
			return err
		}},
		{"failure keywords", func() error {
			value, err := store.FailureKeywords()
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
			tx.Rollback()
			<-done
			t.Fatalf("%s read waited for the writer connection", check.name)
		}
	}
}

func TestStabilityDefaultsValidationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.sqlite")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db}
	value, err := s.Get()
	if err != nil || value.StabilitySeconds != 30 {
		t.Fatal(value, err)
	}
	for _, seconds := range []int{-1, 3601} {
		value.StabilitySeconds = seconds
		if err = s.Save(value); err == nil {
			t.Fatal("invalid wait accepted", seconds)
		}
	}
	value.StabilitySeconds = 0
	if err = s.Save(value); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	value, err = (Store{DB: db}).Get()
	if err != nil || value.StabilitySeconds != 0 {
		t.Fatal(value, err)
	}
	value.StabilitySeconds = 3600
	if err = (Store{DB: db}).Save(value); err != nil {
		t.Fatal(err)
	}
}

func TestFixedPromptDefaultsValidationAndRestart(t *testing.T) {
	if !strings.Contains(DefaultFixedPrompt, "只回复“命名失败”四个字") || !strings.Contains(DefaultFixedPrompt, "此时不适用上述标题字数要求") {
		t.Fatal("默认固定提示词必须包含命名失败协议及字数例外")
	}
	path := filepath.Join(t.TempDir(), "fixed.sqlite")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db}
	fixed, err := s.FixedPrompt()
	if err != nil || fixed != DefaultFixedPrompt {
		t.Fatal(fixed, err)
	}
	if _, err = s.SaveFixedPrompt("  "); err == nil {
		t.Fatal("blank fixed prompt accepted")
	}
	if _, err = s.SaveFixedPrompt(strings.Repeat("字", 16001)); err == nil {
		t.Fatal("oversized fixed prompt accepted")
	}
	saved, err := s.SaveFixedPrompt("只输出标题。")
	if err != nil || saved != "只输出标题。" {
		t.Fatal(saved, err)
	}
	db.Close()
	db, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if fixed, err = (Store{DB: db}).FixedPrompt(); err != nil || fixed != "只输出标题。" {
		t.Fatal(fixed, err)
	}
}

func TestSettingsDoNotHideStorageErrors(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Numbering(); err == nil {
		t.Fatal("closed database returned default numbering")
	}
	if _, err := s.FixedPrompt(); err == nil {
		t.Fatal("closed database returned default prompt")
	}
}

func TestInvalidSavedNumberingFails(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Store{DB: db}
	if err = s.Save(Default()); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"{", `{"image":{"padding":-1}}`} {
		if _, err = db.Exec("UPDATE system_settings SET numbering=?", raw); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Numbering(); err == nil {
			t.Fatalf("invalid rules accepted: %s", raw)
		}
	}
}

func TestRetryAndRetentionSettings(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "policy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Store{DB: db}
	value, err := store.Get()
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
	if err = store.Save(value); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get()
	if err != nil || got != value {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*Settings){func(s *Settings) { s.RetryCount = 11 }, func(s *Settings) { s.RetryCount = 0 }, func(s *Settings) { s.SuccessCacheHours = 0 }, func(s *Settings) { s.IdleCacheHours = -1 }, func(s *Settings) { s.SuccessCacheMode = "invalid" }} {
		invalid := value
		mutate(&invalid)
		if err = store.Save(invalid); err == nil {
			t.Fatal("accepted", invalid)
		}
	}
}

func TestNumberingDefaultsAndSavedChoice(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "numbering.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Store{DB: db}
	rules, err := store.Numbering()
	if err != nil || !rules.Image.Compact || !rules.Audio.Compact {
		t.Fatal(rules, err)
	}
	rules.Image.Compact = false
	rules.Audio.Compact = false
	if err := store.SaveNumbering(rules); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Numbering()
	if err != nil || saved != rules {
		t.Fatal(saved, err)
	}
}

func TestBatchLimitsDefaultsValidationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "limits.sqlite")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	store := Store{DB: db}
	limits, err := store.BatchLimits()
	if err != nil || limits.RunningBatchLimit != 32 || limits.WaitingBatchLimit != 1000 {
		t.Fatal(limits, err)
	}
	value := Default()
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	// Existing policy documents without the optional fields use new defaults.
	if _, err := db.Exec(`UPDATE system_settings SET policy='{"retry_enabled":true,"retry_count":5}'`); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get()
	if err != nil || got.BatchLimits != limits || got.RetryCount != 5 {
		t.Fatal(got, err)
	}
	for _, pair := range [][2]int{{0, 1000}, {257, 1000}, {32, 0}, {32, 100001}} {
		value.RunningBatchLimit, value.WaitingBatchLimit = pair[0], pair[1]
		if err := store.Save(value); err == nil {
			t.Fatal("invalid capacity accepted", pair)
		}
	}
	for _, pair := range [][2]int{{1, 1}, {256, 100000}, {64, 5000}} {
		value.RunningBatchLimit, value.WaitingBatchLimit = pair[0], pair[1]
		if err := store.Save(value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store = Store{DB: db}
	got, err = store.Get()
	if err != nil || got != value {
		t.Fatal(got, err)
	}
	limits, err = store.BatchLimits()
	if err != nil || limits != value.BatchLimits {
		t.Fatal(limits, err)
	}
	if _, err := db.Exec(`UPDATE system_settings SET policy='{"running_batch_limit":0,"waiting_batch_limit":5000}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BatchLimits(); err == nil {
		t.Fatal("invalid stored capacity used")
	}
	if _, err := store.Get(); err == nil {
		t.Fatal("invalid stored capacity shown as valid")
	}
}
