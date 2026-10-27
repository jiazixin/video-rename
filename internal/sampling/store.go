// Package sampling persists the global frame settings used by new batches.
package sampling

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

// Store stores the global frame settings.
type Store struct{ DB *storage.Store }

// Get returns the stored frame settings.
func (s Store) Get(ctx context.Context) (config.FrameSettings, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, "SELECT settings FROM sampling_settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		value := config.DefaultFrameSettings()
		return value, value.Validate()
	}
	if err != nil {
		return config.FrameSettings{}, err
	}
	return Parse(raw)
}

// Parse decodes and validates stored frame settings.
func Parse(raw string) (config.FrameSettings, error) {
	var value config.FrameSettings
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, err
	}
	return value, value.Validate()
}

// Save validates and stores value.
func (s Store) Save(ctx context.Context, value config.FrameSettings) error {
	if err := value.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO sampling_settings(id,settings) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET settings=excluded.settings", string(raw))
	return err
}
