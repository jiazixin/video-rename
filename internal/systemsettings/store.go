// Package systemsettings owns runtime resource limits, independent of model profiles.
package systemsettings

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// DefaultFixedPrompt is the hard constraint appended below the selected
// prompt on every model request; the page may replace the stored value.
const DefaultFixedPrompt = `输出要求：
标题正文不少于25个中文字符，建议控制在30至50个中文字符
最终响应只能包含一个完整的中文标题正文。
必须单行输出。
除标题正文之外不输出任何其他内容。
例外：如果无法根据提供的画面生成符合要求的标题，只回复“命名失败”四个字，不加引号、标点或解释；此时不适用上述标题字数要求。`

// Policy holds the retry, fallback model and frame cache policy.
type Policy struct {
	RetryEnabled      bool   `json:"retry_enabled"`
	RetryCount        int    `json:"retry_count"`
	FallbackModelID   int64  `json:"fallback_model_id"`
	SuccessCacheMode  string `json:"success_cache_mode"`
	SuccessCacheHours int    `json:"success_cache_hours"`
	IdleCacheHours    int    `json:"idle_cache_hours"`
}

// Settings are the runtime resource limits and policies.
type Settings struct {
	Policy
	BatchLimits
	StabilitySeconds int   `json:"stability_seconds"`
	ExtractWorkers   int   `json:"extract_workers"`
	CacheGB          int64 `json:"cache_gb"`
}

// BatchLimits bounds running and waiting batches.
type BatchLimits struct {
	RunningBatchLimit int `json:"running_batch_limit"`
	WaitingBatchLimit int `json:"waiting_batch_limit"`
}

// Validate checks the batch limits.
func (v BatchLimits) Validate() error {
	if v.RunningBatchLimit < 1 || v.RunningBatchLimit > 256 {
		return domain.InvalidField("running_batch_limit", "out_of_range")
	}
	if v.WaitingBatchLimit < 1 || v.WaitingBatchLimit > 100000 {
		return domain.InvalidField("waiting_batch_limit", "out_of_range")
	}
	return nil
}

// ReadBatchLimits also accepts a transaction row so admission and settings saves
// are serialized by the same database writer.
func ReadBatchLimits(row interface{ Scan(...any) error }) (BatchLimits, error) {
	v, err := loadPolicy(row)
	if err != nil {
		return BatchLimits{}, err
	}
	return v.BatchLimits, v.BatchLimits.Validate()
}

// BatchLimits returns the stored batch limits.
func (s Store) BatchLimits() (BatchLimits, error) {
	return ReadBatchLimits(s.DB.Read().QueryRow("SELECT policy FROM system_settings WHERE id=1"))
}

// Default returns the default settings.
func Default() Settings {
	return Settings{ExtractWorkers: 8, CacheGB: 10, StabilitySeconds: 30, RunningBatchLimit: 32, WaitingBatchLimit: 1000, RetryEnabled: true, RetryCount: 3, SuccessCacheMode: "immediate", SuccessCacheHours: 72, IdleCacheHours: 72}
}

// Validate checks the settings.
func (s Settings) Validate() error {
	if err := s.BatchLimits.Validate(); err != nil {
		return err
	}
	if s.RetryCount < 0 || s.RetryCount > 10 || (s.RetryEnabled && s.RetryCount == 0) {
		return domain.InvalidField("retry_count", "out_of_range")
	}
	if s.FallbackModelID < 0 {
		return domain.InvalidField("fallback_model_id", "invalid")
	}
	if s.SuccessCacheMode != "" && s.SuccessCacheMode != "immediate" && s.SuccessCacheMode != "delayed" && s.SuccessCacheMode != "keep" {
		return domain.InvalidField("success_cache_mode", "invalid_choice")
	}
	if s.SuccessCacheHours < 0 || s.SuccessCacheHours > 8760 || (s.SuccessCacheMode == "delayed" && s.SuccessCacheHours == 0) {
		return domain.InvalidField("success_cache_hours", "out_of_range")
	}
	if s.IdleCacheHours < 0 || s.IdleCacheHours > 8760 {
		return domain.InvalidField("idle_cache_hours", "out_of_range")
	}
	if s.ExtractWorkers < 1 || s.ExtractWorkers > 32 {
		return domain.InvalidField("extract_workers", "out_of_range")
	}
	if s.CacheGB < 1 || s.CacheGB > 10000 {
		return domain.InvalidField("cache_gb", "out_of_range")
	}
	if s.StabilitySeconds < 0 || s.StabilitySeconds > 3600 {
		return domain.InvalidField("stability_seconds", "out_of_range")
	}
	return nil
}

// Bytes returns the frame cache limit in bytes.
func (s Settings) Bytes() int64 { return s.CacheGB * 1000000000 }

// Store stores the system settings.
type Store struct{ DB *storage.Store }

// Get returns the stored settings.
func (s Store) Get() (Settings, error) {
	v := Default()
	var policy string
	err := s.DB.Read().QueryRow("SELECT extract_workers,cache_gb,stability_seconds,policy FROM system_settings WHERE id=1").Scan(&v.ExtractWorkers, &v.CacheGB, &v.StabilitySeconds, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err == nil && policy != "" {
		stored := storedPolicy{Policy: v.Policy, BatchLimits: v.BatchLimits}
		err = json.Unmarshal([]byte(policy), &stored)
		v.Policy, v.BatchLimits = stored.Policy, stored.BatchLimits
	}
	if err == nil {
		err = v.BatchLimits.Validate()
	}
	return v, err
}

// Save validates and stores v.
func (s Store) Save(v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	policy, err := loadPolicy(tx.QueryRow("SELECT policy FROM system_settings WHERE id=1"))
	if err != nil {
		return err
	}
	policy.Policy = v.Policy
	policy.BatchLimits = v.BatchLimits
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,policy) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET extract_workers=excluded.extract_workers,cache_gb=excluded.cache_gb,stability_seconds=excluded.stability_seconds,policy=excluded.policy", v.ExtractWorkers, v.CacheGB, v.StabilitySeconds, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

// Numbering returns the persisted numbering rules; empty or missing values
// use the defaults until the user saves rules.
func (s Store) Numbering() (domain.NumberingSettings, error) {
	v := domain.DefaultNumberingSettings()
	var raw string
	err := s.DB.Read().QueryRow("SELECT numbering FROM system_settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if raw == "" {
		return v, nil
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, fmt.Errorf("stored numbering settings: %w", err)
	}
	return v, domain.ValidateNumberingSettings(v)
}

// SaveNumbering validates and stores the numbering settings v.
func (s Store) SaveNumbering(v domain.NumberingSettings) error {
	if err := domain.ValidateNumberingSettings(v); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,numbering) VALUES(1,8,10,30,?) ON CONFLICT(id) DO UPDATE SET numbering=excluded.numbering", string(raw))
	return err
}

// FixedPrompt returns the stored hard-constraint suffix; empty or missing
// values fall back to the default text, which includes the naming-failure
// output protocol.
func (s Store) FixedPrompt() (string, error) {
	var v string
	err := s.DB.Read().QueryRow("SELECT fixed_prompt FROM system_settings WHERE id=1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultFixedPrompt, nil
	}
	if err != nil {
		return "", err
	}
	if v == "" {
		return DefaultFixedPrompt, nil
	}
	return v, err
}

// SaveFixedPrompt stores the fixed prompt appended to every request and returns
// it trimmed.
func (s Store) SaveFixedPrompt(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 16000 {
		return "", domain.InvalidField("fixed_prompt", "invalid_length")
	}
	if _, err := s.DB.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,numbering,fixed_prompt) VALUES(1,8,10,30,'',?) ON CONFLICT(id) DO UPDATE SET fixed_prompt=excluded.fixed_prompt", v); err != nil {
		return "", err
	}
	return s.FixedPrompt()
}
