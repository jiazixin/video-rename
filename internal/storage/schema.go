package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
)

//go:embed schema.sql
var schema string

const schemaVersion = 20261028

// initialize creates the current schema once. An unrelated or obsolete database
// is rejected before any schema changes; its contents are never reset or upgraded.
func (s *Store) initialize(ctx context.Context) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var version, tables int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version == schemaVersion {
			return nil
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if version != 0 || tables != 0 {
			return errors.New("unsupported database schema; configure a new storage directory for this version; the existing database was not modified")
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("create database schema: %w", err)
		}
		return nil
	})
}

// inspectSchema rejects existing unsupported files using a read-only connection,
// before writer pragmas can change the journal mode or any schema metadata.
func inspectSchema(ctx context.Context, path url.URL) (err error) {
	info, err := os.Stat(path.Path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && info.Size() == 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	path.RawQuery = "mode=ro&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", path.String())
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database schema inspection: %w", closeErr))
		}
	}()
	var version, tables int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("inspect database version: %w", err)
	}
	if version == schemaVersion {
		return nil
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		return fmt.Errorf("inspect database tables: %w", err)
	}
	if version != 0 || tables != 0 {
		return errors.New("unsupported database schema; configure a new storage directory for this version; the existing database was not modified")
	}
	return nil
}
