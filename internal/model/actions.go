package model

import (
	"context"
	"database/sql"
	"errors"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// Duplicate keeps credentials server-side and never copies the global default flag.
func (s Store) Duplicate(ctx context.Context, id int64) (Profile, error) {
	next, err := storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (int64, error) {
		var err error
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM model_configs").Scan(&count); err != nil {
			return int64(0), err
		}
		if count >= 200 {
			return int64(0), domain.Fail(domain.KindTooMany, domain.Record{Code: "model_configs_full", Message: "at most 200 model configurations"})
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO model_configs(title,prompt_id,created,updated,settings,api_key) SELECT substr(title,1,75)||' 副本',prompt_id,?,?,settings,api_key FROM model_configs WHERE id=?`, storage.Now(), storage.Now(), id)
		if err != nil {
			return int64(0), err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return int64(0), domain.Fail(domain.KindNotFound, domain.Record{Code: "model_config_not_found", Message: "model configuration does not exist"})
		}
		next, err := res.LastInsertId()
		if err != nil {
			return int64(0), err
		}
		return next, nil
	})
	if err != nil {
		return Profile{}, err
	}
	return s.Get(ctx, next)
}

// Delete deletes model profile id if its version is still version.
func (s Store) Delete(ctx context.Context, id, version int64) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		var current, selected int64
		if err = tx.QueryRowContext(ctx, "SELECT version,is_default FROM model_configs WHERE id=?", id).Scan(&current, &selected); errors.Is(err, sql.ErrNoRows) {
			return domain.Fail(domain.KindNotFound, domain.Record{Code: "model_config_not_found", Message: "model configuration does not exist"})
		}
		if err != nil {
			return err
		}
		if current != version {
			return domain.Fail(domain.KindConflict, domain.Record{Code: "model_config_changed", Message: "model configuration was modified; refresh"})
		}
		if selected != 0 {
			return domain.Fail(domain.KindConflict, domain.Record{Code: "model_config_is_default", Message: "the default model configuration cannot be deleted"})
		}
		var fallback int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM system_settings WHERE json_extract(NULLIF(policy,''),'$.fallback_model_id')=?", id).Scan(&fallback); err != nil {
			return err
		}
		if fallback > 0 {
			return domain.Fail(domain.KindConflict, domain.Record{Code: "model_config_is_fallback", Message: "the fallback model configuration cannot be deleted"})
		}
		var plans int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM schedules WHERE model_config_id=?", id).Scan(&plans); err != nil {
			return err
		}
		if plans > 0 {
			return domain.Fail(domain.KindConflict, domain.Record{Code: "model_config_in_use", Message: "model configuration is used by schedules", Details: map[string]any{"plans": plans}})
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM model_configs WHERE id=?", id); err != nil {
			return err
		}
		return nil
	})
}
