package models

import (
	"context"
	"encoding/json/v2"

	"video-rename/internal/domain"
)

// Workers reads only the live scheduling limit, without loading credentials or
// replacing the batch's immutable request settings.
func (s Store) Workers(ctx context.Context, id int64) (int, error) {
	var raw string
	if err := s.DB.Read().QueryRowContext(ctx, "SELECT settings FROM model_configs WHERE id=?", id).Scan(&raw); err != nil {
		return 0, err
	}
	settings := struct {
		Workers int `json:"workers"`
	}{Workers: 2}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return 0, err
	}
	if settings.Workers == 0 {
		settings.Workers = 2
	}
	if settings.Workers < 1 || settings.Workers > 32 {
		return 0, domain.InvalidField("workers", "out_of_range")
	}
	return settings.Workers, nil
}
