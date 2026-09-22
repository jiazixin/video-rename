// Package systemsettings owns runtime resource limits, independent of model profiles.
package systemsettings

import (
	"database/sql"
	"errors"
	"fmt"
	"video-rename/internal/storage"
)

type Settings struct {
	StabilitySeconds int   `json:"stability_seconds"`
	ExtractWorkers   int   `json:"extract_workers"`
	CacheGB          int64 `json:"cache_gb"`
}

func Default() Settings { return Settings{ExtractWorkers: 8, CacheGB: 10, StabilitySeconds: 30} }
func (s Settings) Validate() error {
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
	err := s.DB.QueryRow("SELECT extract_workers,cache_gb,stability_seconds FROM system_settings WHERE id=1").Scan(&v.ExtractWorkers, &v.CacheGB, &v.StabilitySeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	return v, err
}
func (s Store) Save(v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	_, err := s.DB.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,stability_seconds) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET extract_workers=excluded.extract_workers,cache_gb=excluded.cache_gb,stability_seconds=excluded.stability_seconds", v.ExtractWorkers, v.CacheGB, v.StabilitySeconds)
	return err
}
