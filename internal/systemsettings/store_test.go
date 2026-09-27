package systemsettings

import (
	"path/filepath"
	"strings"
	"testing"
	"video-rename/internal/storage"
)

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
