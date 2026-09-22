package models

import (
	"fmt"
	"video-rename/internal/storage"
)

// Duplicate keeps credentials server-side and never copies the global default flag.
func (s Store) Duplicate(id int64) (Profile, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM model_configs").Scan(&count); err != nil {
		return Profile{}, err
	}
	if count >= 200 {
		return Profile{}, fmt.Errorf("最多保存 200 条模型配置")
	}
	res, err := tx.Exec(`INSERT INTO model_configs(title,body,created,updated,settings,api_key) SELECT substr(title,1,75)||' 副本',body,?,?,settings,api_key FROM model_configs WHERE id=?`, storage.Now(), storage.Now(), id)
	if err != nil {
		return Profile{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Profile{}, fmt.Errorf("模型配置不存在")
	}
	next, err := res.LastInsertId()
	if err != nil {
		return Profile{}, err
	}
	if err = tx.Commit(); err != nil {
		return Profile{}, err
	}
	return s.Get(next)
}
func (s Store) Delete(id, version int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current, selected int64
	if err = tx.QueryRow("SELECT version,is_default FROM model_configs WHERE id=?", id).Scan(&current, &selected); err != nil {
		return fmt.Errorf("模型配置不存在")
	}
	if current != version {
		return fmt.Errorf("模型配置已修改，请刷新后重试")
	}
	if selected != 0 {
		return fmt.Errorf("请先将其他模型配置设为全局默认，再删除此配置")
	}
	var plans int
	if err = tx.QueryRow("SELECT count(*) FROM schedules WHERE model_config_id=?", id).Scan(&plans); err != nil {
		return err
	}
	if plans > 0 {
		return fmt.Errorf("有 %d 个运行计划使用此配置，请先更换计划中的模型配置", plans)
	}
	if _, err = tx.Exec("DELETE FROM model_configs WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
