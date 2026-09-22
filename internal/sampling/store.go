// Package sampling persists the global frame settings used by new batches.
package sampling

import (
	"database/sql"
	"encoding/json"
	"errors"
	"video-rename/internal/config"
	"video-rename/internal/storage"
)

type Store struct{ DB *storage.Store }

func (s Store) Get() (config.FrameSettings, error) {
	var raw string
	err := s.DB.QueryRow("SELECT settings FROM sampling_settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		value := config.DefaultFrameSettings()
		return value, value.Validate()
	}
	if err != nil {
		return config.FrameSettings{}, err
	}
	return Parse(raw)
}

func Parse(raw string) (config.FrameSettings, error) {
	var value config.FrameSettings
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, err
	}
	return value, value.Validate()
}

func (s Store) Save(value config.FrameSettings) error {
	if err := value.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO sampling_settings(id,settings) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET settings=excluded.settings", string(raw))
	return err
}
