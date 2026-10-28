//go:build scale

package library

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/storage"
)

// A large synthetic index isolates query cost without creating 500,000 files.
// Both paths use the application's modernc SQLite connection and durability
// settings; the measured storage is local and does not represent NAS throughput.
func TestScaleDirectoryScanCleanupWithLargeIndex(t *testing.T) {
	const files = 500000
	const directories = 100
	l, root, _ := fixture(t)
	for n := range directories {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("empty-%03d", n)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "bulk"), 0o700); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	bulk := entry(t, l, 1, "bulk").ID
	parents := make([]int64, directories)
	for n := range directories {
		parents[n] = entry(t, l, 1, fmt.Sprintf("empty-%03d", n)).ID
	}
	start := time.Now()
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `WITH RECURSIVE n(value) AS
	 (VALUES(1) UNION ALL SELECT value+1 FROM n WHERE value<?)
	 INSERT INTO entries(parent,name,kind,identity,size,mtime_ns,observed)
	 SELECT ?,printf('synthetic-%06d.mp4',value),'file',printf('synthetic:%d',value),1048576,0,0 FROM n`, files, bulk)
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d synthetic files in %s", files, time.Since(start))
	const legacyItems = `DELETE FROM directory_items WHERE rowid IN
	 (SELECT rowid FROM directory_items WHERE dir=? AND generation<>?
	 AND generation<>COALESCE((SELECT generation FROM directory_state WHERE dir=?),0) LIMIT ?)`
	const legacyCandidates = `DELETE FROM entries WHERE id IN
	 (SELECT id FROM entries WHERE parent=? AND present=0 AND observed LIKE 'directory-scan:%'
	 AND NOT EXISTS(SELECT 1 FROM directory_items i WHERE i.dir=entries.parent AND i.entry=entries.id) LIMIT ?)`
	start = time.Now()
	for _, parent := range parents {
		if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), legacyItems, parent, 0, parent, refreshPageSize)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), legacyCandidates, parent, refreshPageSize)
		}); err != nil {
			t.Fatal(err)
		}
	}
	legacyCleanup := time.Since(start)
	start = time.Now()
	scanner, err := l.NewDirectoryScanner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scanner.Close(); err != nil {
			t.Error(err)
		}
	})
	capture := time.Since(start)
	start = time.Now()
	for _, parent := range parents {
		if err := l.cleanScanStaging(t.Context(), cleanScanStagingOptions{Stage: scanner.stage, ID: parent, Gen: 0, Marker: "directory-scan:scale", Published: true}); err != nil {
			t.Fatal(err)
		}
	}
	indexedCleanup := time.Since(start)
	t.Logf("%d-directory empty cleanup: legacy=%s, indexed=%s, once-per-round orphan capture=%s, indexed total=%s", directories, legacyCleanup, indexedCleanup, capture, indexedCleanup+capture)
	if err := l.db.WriteScan(t.Context(), scanner.stage.Path(), func(tx *sql.Tx) error {
		for _, path := range []struct {
			name, query string
		}{
			{"legacy", "UPDATE entries SET present=1,observed=? WHERE parent=? AND observed=? AND present=0"},
			{"indexed", promoteScanCandidatesSQL},
		} {
			start = time.Now()
			for _, parent := range parents {
				if _, err := tx.ExecContext(t.Context(), path.query, "now", parent, "directory-scan:scale"); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("%d empty candidate promotions (%s): %s", directories, path.name, time.Since(start))
		}
		rows, err := tx.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+promoteScanCandidatesSQL, "now", parents[0], "directory-scan:scale")
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("candidate promotion plan:\n%s", plan.String())
		if strings.Contains(plan.String(), "SCAN entries") || !strings.Contains(plan.String(), "SEARCH entries USING INTEGER PRIMARY KEY") {
			t.Fatalf("candidate promotion scans the complete index: %s", plan.String())
		}
		var count int
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM entries").Scan(&count); err != nil || count != files+directories+2 {
			t.Fatalf("empty cleanup changed indexed entries: count=%d err=%v", count, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
