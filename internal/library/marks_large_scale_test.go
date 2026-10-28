//go:build scale && integration

package library

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Uses real temporary files and a fresh local database, not a production SMB mount.
// The separate tag keeps this expensive test out of the regular scale suite.
func TestScaleLargeProcessedMarks(t *testing.T) {
	const directories = 100
	const filesPerDirectory = 5000
	const totalFiles = directories * filesPerDirectory
	l, root, _ := fixture(t)
	start := time.Now()
	for d := range directories {
		dir := filepath.Join(root, fmt.Sprintf("mark-%03d", d))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for f := range filesPerDirectory {
			put(t, filepath.Join(dir, fmt.Sprintf("clip-%05d.mp4", f)))
		}
	}
	t.Logf("created %d real files in %d directories: %s", totalFiles, directories, time.Since(start))

	start = time.Now()
	result := l.SetProcessed(t.Context(), []int64{1}, true)
	t.Logf("recursive root marking: %+v, elapsed=%s", result, time.Since(start))
	if result.Changed != totalFiles || result.Unchanged != 0 || result.Failed != 0 || result.Interrupted {
		t.Fatalf("incomplete marking: %+v", result)
	}
	for _, check := range []struct {
		name  string
		query string
		want  int
	}{
		{"marks", "SELECT count(*) FROM marks", totalFiles},
		{"indexed files", "SELECT count(*) FROM entries WHERE kind='file' AND present=1", totalFiles},
		{"unmarked files", `SELECT count(*) FROM entries e
			LEFT JOIN marks m ON m.identity=e.identity AND m.size=e.size AND m.mtime_ns=e.mtime_ns
			WHERE e.kind='file' AND e.present=1 AND m.identity IS NULL`, 0},
	} {
		var count int
		if err := l.db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), check.query).Scan(&count) }); err != nil {
			t.Fatal(err)
		}
		if count != check.want {
			t.Fatalf("%s=%d, want %d", check.name, count, check.want)
		}
	}

	var integrity string
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity)
	}); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("database integrity check: %s", integrity)
	}
	if err := l.db.View(t.Context(), func(tx *sql.Tx) (err error) {
		rows, err := tx.QueryContext(t.Context(), "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, rows.Close()) }()
		if rows.Next() {
			return errors.New("database foreign key check found a violation")
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("exact mark counts, database integrity, and foreign keys verified")
}
