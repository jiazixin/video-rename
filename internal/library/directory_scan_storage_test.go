package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/storage"
)

func TestScanStageRollsBackFailedAndCancelledWrites(t *testing.T) {
	t.Parallel()
	stage, err := newScanStage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	failed := errors.New("injected stage failure")
	err = stage.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO orphan_candidates VALUES(2,1,'rollback')"); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("failed write: got %v, want injected failure", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = stage.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO orphan_candidates VALUES(3,1,'cancelled')"); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write: got %v, want context cancellation", err)
	}
	var count int
	if err := stage.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM orphan_candidates").Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("rolled back candidates: got count=%d error=%v, want 0 and nil", count, err)
	}
}

func TestScanStageUsesReadOnlyViewsAndCleansPrivateDirectory(t *testing.T) {
	t.Parallel()
	stage, err := newScanStage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	path := stage.Path()
	for path, mode := range map[string]fs.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("staging permissions: got %o, want %o", info.Mode().Perm(), mode)
		}
	}
	if err := stage.View(t.Context(), func(tx *sql.Tx) error {
		var journal string
		if err := tx.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal); err != nil {
			return err
		}
		if journal != "wal" {
			return fmt.Errorf("scan journal: got %q, want wal", journal)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := stage.View(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "INSERT INTO orphan_candidates VALUES(2,1,'read-only')")
		return err
	}); err == nil {
		t.Fatal("read-only scan view accepted a write")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("closed stage directory: got %v, want not exist", err)
	}
}

func TestScanStageInitializationFailureCleansPrivateDirectory(t *testing.T) {
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	if stage, err := storage.NewScan(t.Context(), "CREATE TABLE partial(value INTEGER); INVALID SQL"); err == nil || stage != nil {
		t.Fatalf("invalid scan schema: got stage=%v error=%v, want nil and error", stage, err)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed stage initialization retained files: got %d entries, error=%v", len(entries), err)
	}
}

func TestScanStageDiagnosticsKeepNanosecondDifferences(t *testing.T) {
	t.Parallel()
	stage, err := newScanStage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	const before int64 = 1700000000123456789
	const after = before + 1
	if err := stage.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO snapshot(name,kind,identity,size,mtime_ns) VALUES('video.mp4','file','1:2',1,?)", before); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), "INSERT INTO verification VALUES('video.mp4','file','1:2',1,?)", after)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = scanMetadataDifference(t.Context(), stage)
	if !errors.Is(err, ErrDirectoryScanChanged) || !strings.Contains(err.Error(), fmt.Sprint(before)) || !strings.Contains(err.Error(), fmt.Sprint(after)) {
		t.Fatalf("nanosecond scan mismatch: got %v, want both exact timestamps", err)
	}
}

func TestScanDirectoryJoinsFailureRecordError(t *testing.T) {
	t.Parallel()
	l, _, _ := fixture(t)
	if err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER reject_scan_error BEFORE INSERT ON directory_state
	 BEGIN SELECT RAISE(ABORT,'failure record rejected'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("injected directory read failure")
	_, err := l.scanDirectory(t.Context(), 1, func(*os.File, int) ([]os.DirEntry, error) {
		return nil, failed
	})
	if !errors.Is(err, failed) || !strings.Contains(err.Error(), "failure record rejected") {
		t.Fatalf("scan and record errors: got %v, want both failures", err)
	}
}

func TestScanDirectoryJoinsCleanupFailure(t *testing.T) {
	t.Parallel()
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "new.mp4"))
	if err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER reject_scan_publish BEFORE INSERT ON directory_state
	 WHEN NEW.error='' BEGIN SELECT RAISE(ABORT,'publication rejected'); END;
	 CREATE TRIGGER reject_scan_cleanup BEFORE DELETE ON entries
	 WHEN OLD.observed LIKE 'directory-scan:%' BEGIN SELECT RAISE(ABORT,'cleanup rejected'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := l.ScanDirectory(t.Context(), 1)
	if err == nil || !strings.Contains(err.Error(), "publication rejected") || !strings.Contains(err.Error(), "cleanup rejected") {
		t.Fatalf("scan and cleanup errors: got %v, want both failures", err)
	}
	var candidates, items int
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT
	 (SELECT count(*) FROM entries WHERE present=0 AND observed LIKE 'directory-scan:%'),
	 (SELECT count(*) FROM directory_items)`).Scan(&candidates, &items)
	}); err != nil || candidates != 1 || items != 1 {
		t.Fatalf("failed cleanup retained recoverable page: got candidates=%d items=%d error=%v", candidates, items, err)
	}
}
