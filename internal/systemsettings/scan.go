package systemsettings

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
)

// ScanSettings controls discovery only; it never starts media processing.
type ScanSettings struct {
	Enabled         bool    `json:"enabled"`
	IntervalMinutes int     `json:"interval_minutes"`
	Targets         []int64 `json:"targets"`
	ExcludedTargets []int64 `json:"excluded_targets"`
}

func DefaultScanSettings() ScanSettings {
	return ScanSettings{Enabled: true, IntervalMinutes: 60, Targets: []int64{1}, ExcludedTargets: []int64{}}
}

func (v ScanSettings) Validate() error {
	if v.IntervalMinutes < 5 || v.IntervalMinutes > 1440 {
		return fmt.Errorf("扫描间隔须在 5–1440 分钟之间")
	}
	if len(v.Targets) == 0 || len(v.Targets) > 100 {
		return fmt.Errorf("请选择 1–100 个扫描目录")
	}
	seen := make(map[int64]bool, len(v.Targets))
	for _, id := range v.Targets {
		if id <= 0 || seen[id] {
			return fmt.Errorf("扫描目录编号无效或重复")
		}
		seen[id] = true
	}
	if len(v.ExcludedTargets) > 100 {
		return fmt.Errorf("最多选择 100 个排除目录")
	}
	clear(seen)
	for _, id := range v.ExcludedTargets {
		if id <= 0 || seen[id] {
			return fmt.Errorf("排除目录编号无效或重复")
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

func (s Store) Scan() (ScanSettings, error) {
	policy, err := loadPolicy(s.DB.Read().QueryRow("SELECT policy FROM system_settings WHERE id=1"))
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
}

func (s Store) SaveScan(v ScanSettings) error {
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
	policy.LibraryScan = new(v)
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	d := Default()
	if _, err = tx.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds,policy) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET policy=excluded.policy", d.ExtractWorkers, d.CacheGB, d.StabilitySeconds, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
