package systemsetting

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"strings"
	"unicode/utf8"

	"video-rename/internal/domain"
	"video-rename/internal/naming"
	"video-rename/internal/storage"
)

// DefaultFailureKeywords returns the default naming failure keywords.
func DefaultFailureKeywords() []string { return naming.DefaultFailureKeywords() }

// NormalizeFailureKeywords validates first, trims entries, and keeps the first
// spelling when the same phrase is repeated with different letter case.
func NormalizeFailureKeywords(values []string) ([]string, error) {
	if values == nil {
		return nil, domain.InvalidField("failure_keywords", "required")
	}
	if len(values) > 100 {
		return nil, domain.InvalidField("failure_keywords", "too_many")
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 200 {
			return nil, domain.InvalidField("failure_keywords", "invalid_item")
		}
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out, nil
}

// FailureKeywords returns the stored naming failure keywords.
func (s Store) FailureKeywords(ctx context.Context) ([]string, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) ([]string, error) {
		policy, err := loadPolicy(tx.QueryRowContext(ctx, "SELECT policy FROM system_settings WHERE id=1"))
		if err != nil {
			return nil, err
		}
		if policy.FailureKeywords == nil {
			return DefaultFailureKeywords(), nil
		}
		return NormalizeFailureKeywords(policy.FailureKeywords)
	})
}

// SaveFailureKeywords normalizes and stores values and returns the stored
// keywords.
func (s Store) SaveFailureKeywords(ctx context.Context, values []string) ([]string, error) {
	keywords, err := NormalizeFailureKeywords(values)
	if err != nil {
		return nil, err
	}
	return storage.WriteValue(ctx, s.db, func(tx *sql.Tx) ([]string, error) {
		var err error
		policy, err := loadPolicy(tx.QueryRowContext(ctx, "SELECT policy FROM system_settings WHERE id=1"))
		if err != nil {
			return nil, err
		}
		policy.FailureKeywords = keywords
		raw, err := json.Marshal(policy)
		if err != nil {
			return nil, err
		}
		d := Default()
		if _, err = tx.ExecContext(ctx, "INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,policy) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET policy=excluded.policy", d.ExtractWorkers, d.CacheGB, d.StabilitySeconds, string(raw)); err != nil {
			return nil, err
		}
		return keywords, nil
	})
}
