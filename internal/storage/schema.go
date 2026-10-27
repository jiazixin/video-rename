package storage

import (
	_ "embed"
	"errors"
	"fmt"
)

//go:embed schema.sql
var schema string

const schemaVersion = 20261027

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
		return errors.New("unsupported database schema; configure a new storage directory for this version; the existing database was not modified")
	}
	if _, err = tx.Exec(schema); err != nil {
		return fmt.Errorf("create database schema: %w", err)
	}
	return tx.Commit()
}
