package storage

import (
	_ "embed"
	"fmt"
)

//go:embed schema.sql
var schema string

const schemaVersion = 20260928

// initialize creates the current schema once. An unrelated or obsolete database
// is rejected before any schema changes; its contents are never reset or upgraded.
func (s *Store) initialize() error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version, tables int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == schemaVersion {
		return tx.Commit()
	}
	if err = tx.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		return err
	}
	if version != 0 || tables != 0 {
		return fmt.Errorf("数据库结构不受支持，请为当前版本配置新的 storage 目录；原数据库未修改")
	}
	if _, err = tx.Exec(schema); err != nil {
		return fmt.Errorf("创建数据库结构: %w", err)
	}
	return tx.Commit()
}
