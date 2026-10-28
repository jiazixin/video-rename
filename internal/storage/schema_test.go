package storage

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchingVersionRequiresCurrentSchema(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"missing index", "DROP INDEX entry_parent_id"},
		{"missing table", "DROP TABLE request_results"},
		{"extra column", "ALTER TABLE prompts ADD COLUMN obsolete TEXT"},
		{"extra table", "CREATE TABLE obsolete(value TEXT)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.sqlite")
			store, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err = db.ExecContext(t.Context(), tc.change+"; PRAGMA journal_mode=DELETE"); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			requireSchemaRejectedWithoutChanges(t, path)
		})
	}
}

func TestUnversionedNonSQLiteTableRemainsIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unrelated.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	// Only sqlite_ is reserved. A LIKE pattern would treat its underscore as a
	// wildcard and misidentify this user table as an internal SQLite object.
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE sqlitex(value TEXT); INSERT INTO sqlitex VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	requireSchemaRejectedWithoutChanges(t, path)
}

func requireSchemaRejectedWithoutChanges(t *testing.T, path string) {
	t.Helper()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err == nil {
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("got accepted unsupported schema, want rejection")
	}
	if !strings.Contains(err.Error(), "unsupported database schema") {
		t.Fatalf("got %v, want unsupported database schema", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("got modified database bytes after schema rejection, want original bytes")
	}
}
