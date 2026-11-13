//go:build integration && scale

package library

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Match many-small-directory workloads without accessing the user's media.
func TestScaleLargeProcessedMarksManyDirectories(t *testing.T) {
	const directories, filesPerDirectory = 10000, 53
	const total = int64(directories * filesPerDirectory)
	l, root, _ := fixture(t)
	started := time.Now()
	for d := range directories {
		dir := filepath.Join(root, fmt.Sprintf("dir-%05d", d))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for f := range filesPerDirectory {
			put(t, filepath.Join(dir, fmt.Sprintf("%03d.mp4", f)))
		}
	}
	t.Logf("created %d files across %d directories in %s", total, directories, time.Since(started))
	var logs bytes.Buffer
	l.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	for _, phase := range []string{"first_index", "existing_index"} {
		if err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), "DELETE FROM marks")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		logs.Reset()
		result := l.SetProcessed(t.Context(), []int64{1}, true)
		t.Logf("%s: %s", phase, logs.String())
		if result.Changed != total || result.Unchanged != 0 || result.Failed != 0 || result.Interrupted {
			t.Fatalf("%s: incomplete marks: %+v", phase, result)
		}
		if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
			var marked, indexed int64
			if err := tx.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM marks),
 (SELECT count(*) FROM entries WHERE present=1 AND kind='file')`).Scan(&marked, &indexed); err != nil {
				return err
			}
			if marked != total || indexed != total {
				return fmt.Errorf("got %d marks and %d files, want %d each", marked, indexed, total)
			}
			var integrity string
			if err := tx.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil {
				return err
			}
			if integrity != "ok" {
				return fmt.Errorf("integrity check: %s", integrity)
			}
			rows, err := tx.QueryContext(t.Context(), "PRAGMA foreign_key_check")
			if err != nil {
				return err
			}
			if rows.Next() {
				return errors.Join(errors.New("foreign key violation"), rows.Close())
			}
			return errors.Join(rows.Err(), rows.Close())
		}); err != nil {
			t.Fatalf("%s: %v", phase, err)
		}
	}
}
