package systemsettings

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"unicode/utf8"

	"video-rename/internal/naming"
)

func DefaultFailureKeywords() []string { return naming.DefaultFailureKeywords() }

// NormalizeFailureKeywords validates first, trims entries, and keeps the first
// spelling when the same phrase is repeated with different letter case.
func NormalizeFailureKeywords(values []string) ([]string, error) {
	if values == nil {
		return nil, fmt.Errorf("请提供失败词句数组；空数组表示关闭匹配")
	}
	if len(values) > 100 {
		return nil, fmt.Errorf("最多保存 100 条失败词句")
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 200 {
			return nil, fmt.Errorf("每条失败词句须为 1–200 字，不能只包含空白")
		}
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out, nil
}

func (s Store) FailureKeywords() ([]string, error) {
	policy, err := loadPolicy(s.DB.Read().QueryRow("SELECT policy FROM system_settings WHERE id=1"))
	if err != nil {
		return nil, err
	}
	if policy.FailureKeywords == nil {
		return DefaultFailureKeywords(), nil
	}
	return NormalizeFailureKeywords(policy.FailureKeywords)
}

func (s Store) SaveFailureKeywords(values []string) ([]string, error) {
	keywords, err := NormalizeFailureKeywords(values)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	policy, err := loadPolicy(tx.QueryRow("SELECT policy FROM system_settings WHERE id=1"))
	if err != nil {
		return nil, err
	}
	policy.FailureKeywords = keywords
	raw, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	d := Default()
	if _, err = tx.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,policy) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET policy=excluded.policy", d.ExtractWorkers, d.CacheGB, d.StabilitySeconds, string(raw)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return keywords, nil
}
