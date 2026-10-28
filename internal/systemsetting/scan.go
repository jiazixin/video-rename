package systemsetting

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// ScanSettings controls discovery only; it never starts media processing.
type ScanSettings struct {
	Enabled         bool    `json:"enabled"`
	IntervalMinutes int     `json:"interval_minutes"`
	Targets         []int64 `json:"targets"`
	ExcludedTargets []int64 `json:"excluded_targets"`
}

// DefaultScanSettings returns the default background scan settings.
func DefaultScanSettings() ScanSettings {
	return ScanSettings{Enabled: true, IntervalMinutes: 60, Targets: []int64{1}, ExcludedTargets: []int64{}}
}

// Validate checks the scan settings.
func (v ScanSettings) Validate() error {
	if v.IntervalMinutes < 5 || v.IntervalMinutes > 1440 {
		return domain.InvalidField("interval_minutes", "out_of_range")
	}
	if len(v.Targets) == 0 || len(v.Targets) > 100 {
		return domain.InvalidField("targets", "selection_required")
	}
	seen := make(map[int64]bool, len(v.Targets))
	for _, id := range v.Targets {
		if id <= 0 || seen[id] {
			return domain.InvalidField("targets", "invalid_or_duplicate")
		}
		seen[id] = true
	}
	if len(v.ExcludedTargets) > 100 {
		return domain.InvalidField("excluded_targets", "too_many")
	}
	clear(seen)
	for _, id := range v.ExcludedTargets {
		if id <= 0 || seen[id] {
			return domain.InvalidField("excluded_targets", "invalid_or_duplicate")
		}
		seen[id] = true
	}
	return nil
}

// The scan field shares the existing policy document, with independent updates.
// This keeps the deployed schema and all previously saved processing settings.
type storedPolicy struct {
	Policy
	BatchLimits
	LibraryScan     *ScanSettings `json:"library_scan,omitzero"`
	FailureKeywords []string      `json:"failure_keywords,omitzero"`
}

func loadPolicy(row interface{ Scan(...any) error }) (storedPolicy, error) {
	d := Default()
	v := storedPolicy{Policy: d.Policy, BatchLimits: d.BatchLimits}
	var raw string
	err := row.Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return v, err
}

// Scan returns the stored background scan settings.
func (s Store) Scan(ctx context.Context) (ScanSettings, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (ScanSettings, error) {
		policy, err := loadPolicy(tx.QueryRowContext(ctx, "SELECT policy FROM system_settings WHERE id=1"))
		if err != nil {
			return ScanSettings{}, err
		}
		v := DefaultScanSettings()
		if policy.LibraryScan != nil {
			v = *policy.LibraryScan
			v.Targets = slices.Clone(v.Targets)
			v.ExcludedTargets = append([]int64{}, v.ExcludedTargets...)
		}
		return v, v.Validate()
	})
}

// SaveScan validates and stores v.
func (s Store) SaveScan(ctx context.Context, v ScanSettings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		policy, err := loadPolicy(tx.QueryRowContext(ctx, "SELECT policy FROM system_settings WHERE id=1"))
		if err != nil {
			return err
		}
		policy.LibraryScan = new(v)
		raw, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		d := Default()
		if _, err = tx.ExecContext(ctx, "INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,policy) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET policy=excluded.policy", d.ExtractWorkers, d.CacheGB, d.StabilitySeconds, string(raw)); err != nil {
			return err
		}
		return nil
	})
}
