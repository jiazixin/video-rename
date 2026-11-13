package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestScanDurabilityIsIsolatedFromMainDatabase(t *testing.T) {
	t.Parallel()
	stage, err := NewScan(t.Context(), "CREATE TABLE observations(value INTEGER)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	// Recreated writer connections must get the same disposable-stage policy.
	stage.write.SetMaxIdleConns(0)
	for range 2 {
		if err := stage.Write(t.Context(), func(tx *sql.Tx) error {
			var synchronous int
			if err := tx.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&synchronous); err != nil {
				return err
			}
			if synchronous != 1 {
				t.Errorf("scan synchronous=%d, want NORMAL (1)", synchronous)
			}
			_, err := tx.ExecContext(t.Context(), "INSERT INTO observations VALUES(7)")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "CREATE TABLE copied(value INTEGER)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteScan(t.Context(), stage.Path(), func(tx *sql.Tx) error {
		var synchronous int
		if err := tx.QueryRowContext(t.Context(), "PRAGMA main.synchronous").Scan(&synchronous); err != nil {
			return err
		}
		if synchronous != 2 {
			t.Errorf("main synchronous=%d, want FULL (2)", synchronous)
		}
		_, err := tx.ExecContext(t.Context(), "INSERT INTO copied SELECT value FROM directory_scan.observations")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM copied WHERE value=7").Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("published %d observations, want 2 after removing stage", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var synchronous int
	if err := db.write.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		t.Fatalf("main after detaching: synchronous=%d error=%v, want FULL", synchronous, err)
	}
}
