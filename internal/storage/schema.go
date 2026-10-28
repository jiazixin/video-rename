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
	"slices"
)

//go:embed schema.sql
var schema string

const schemaVersion = 2026102801

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
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'").Scan(&tables); err != nil {
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
		return inspectCurrentSchema(ctx, db)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'").Scan(&tables); err != nil {
		return fmt.Errorf("inspect database tables: %w", err)
	}
	if version != 0 || tables != 0 {
		return errors.New("unsupported database schema; configure a new storage directory for this version; the existing database was not modified")
	}
	return nil
}

type schemaObject struct{ kind, name, table, definition string }

// inspectCurrentSchema derives the supported catalog from the same embedded
// source used for creation. A version number alone cannot identify a schema:
// interrupted manual edits or an unreleased build can retain that number.
func inspectCurrentSchema(ctx context.Context, actual *sql.DB) (err error) {
	expected, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return fmt.Errorf("open reference schema: %w", err)
	}
	expected.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, expected.Close()) }()
	if _, err = expected.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create reference schema: %w", err)
	}
	want, err := schemaObjects(ctx, expected)
	if err != nil {
		return fmt.Errorf("read reference schema: %w", err)
	}
	got, err := schemaObjects(ctx, actual)
	if err != nil {
		return fmt.Errorf("inspect database schema: %w", err)
	}
	if !slices.Equal(got, want) {
		return errors.New("unsupported database schema; structure differs from this build; configure a new storage directory; the existing database was not modified")
	}
	return nil
}

func schemaObjects(ctx context.Context, db *sql.DB) ([]schemaObject, error) {
	// SQLite owns its sqlite_* objects (including ANALYZE statistics). Table
	// definitions already include the constraints behind its automatic indexes.
	rows, err := db.QueryContext(ctx, `SELECT type,name,tbl_name,coalesce(sql,'')
 FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*' ORDER BY type,name`)
	if err != nil {
		return nil, err
	}
	return CollectRows(rows, func(rows *sql.Rows) (schemaObject, error) {
		var object schemaObject
		err := rows.Scan(&object.kind, &object.name, &object.table, &object.definition)
		return object, err
	})
}
