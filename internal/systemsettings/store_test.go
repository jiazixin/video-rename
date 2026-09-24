package systemsettings

import (
	"path/filepath"
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

func TestVersionTwelveUpgradePreservesSettingsAndDefaultsWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.sqlite")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb) VALUES(1,3,2); ALTER TABLE system_settings DROP COLUMN stability_seconds; ALTER TABLE events DROP COLUMN processing_log; ALTER TABLE image_numberings DROP COLUMN media; ALTER TABLE system_settings DROP COLUMN numbering; DROP TABLE token_usage_records; DELETE FROM schema_migrations WHERE version>=13"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	db, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	value, err := (Store{DB: db}).Get()
	if err != nil || value.ExtractWorkers != 3 || value.CacheGB != 2 || value.StabilitySeconds != 30 {
		t.Fatal(value, err)
	}
}
