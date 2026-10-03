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

type Policy struct {
	RetryEnabled      bool   `json:"retry_enabled"`
	RetryCount        int    `json:"retry_count"`
	FallbackModelID   int64  `json:"fallback_model_id"`
	SuccessCacheMode  string `json:"success_cache_mode"`
	SuccessCacheHours int    `json:"success_cache_hours"`
	IdleCacheHours    int    `json:"idle_cache_hours"`
}

type Settings struct {
	Policy
	StabilitySeconds int   `json:"stability_seconds"`
	ExtractWorkers   int   `json:"extract_workers"`
	CacheGB          int64 `json:"cache_gb"`
}

func Default() Settings {
	return Settings{ExtractWorkers: 8, CacheGB: 10, StabilitySeconds: 30, Policy: Policy{RetryEnabled: true, RetryCount: 3, SuccessCacheMode: "immediate", SuccessCacheHours: 72, IdleCacheHours: 72}}
}
func (s Settings) Validate() error {
	if s.RetryCount < 0 || s.RetryCount > 10 || (s.RetryEnabled && s.RetryCount == 0) {
		return fmt.Errorf("重试次数须在 1–10 次之间")
	}
	if s.FallbackModelID < 0 {
		return fmt.Errorf("兜底模型配置无效")
	}
	if s.SuccessCacheMode != "" && s.SuccessCacheMode != "immediate" && s.SuccessCacheMode != "delayed" && s.SuccessCacheMode != "keep" {
		return fmt.Errorf("缓存处理方式无效")
	}
	if s.SuccessCacheHours < 0 || s.SuccessCacheHours > 8760 || (s.SuccessCacheMode == "delayed" && s.SuccessCacheHours == 0) {
		return fmt.Errorf("成功后保留时长须在 1–8760 小时之间")
	}
	if s.IdleCacheHours < 0 || s.IdleCacheHours > 8760 {
		return fmt.Errorf("闲置清理时长须在 0–8760 小时之间，0 表示关闭")
	}
	if s.ExtractWorkers < 1 || s.ExtractWorkers > 32 {
		return fmt.Errorf("并发抽帧数须在 1–32 之间")
	}
	if s.CacheGB < 1 || s.CacheGB > 10000 {
		return fmt.Errorf("临时目录容量须在 1–10000 GB 之间")
	}
	if s.StabilitySeconds < 0 || s.StabilitySeconds > 3600 {
		return fmt.Errorf("稳定性等待时长须在 0–3600 秒之间，0 表示禁用等待")
	}
	return nil
}
func (s Settings) Bytes() int64 { return s.CacheGB * 1000000000 }

type Store struct{ DB *storage.Store }

func (s Store) Get() (Settings, error) {
	v := Default()
	var policy string
	err := s.DB.Read().QueryRow("SELECT extract_workers,cache_gb,stability_seconds,policy FROM system_settings WHERE id=1").Scan(&v.ExtractWorkers, &v.CacheGB, &v.StabilitySeconds, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err == nil && policy != "" {
		err = json.Unmarshal([]byte(policy), &v.Policy)
	}
	return v, err
}
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
		return v, fmt.Errorf("编号设置无效: %w", err)
	}
	return v, domain.ValidateNumberingSettings(v)
}
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
func (s Store) SaveFixedPrompt(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 16000 {
		return "", fmt.Errorf("固定提示词不能为空且不得超过 16000 字节")
	}
	if _, err := s.DB.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,numbering,fixed_prompt) VALUES(1,8,10,30,'',?) ON CONFLICT(id) DO UPDATE SET fixed_prompt=excluded.fixed_prompt", v); err != nil {
		return "", err
	}
	return s.FixedPrompt()
}
