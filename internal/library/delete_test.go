package library

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteFileAndDirectory(t *testing.T) {
	l, root, _ := fixture(t)
	outside := filepath.Join(t.TempDir(), "keep.txt")
	put(t, outside)
	if err := os.MkdirAll(filepath.Join(root, "folder", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "folder", "nested", "video.mp4"))
	if err := os.Symlink(outside, filepath.Join(root, "folder", "link")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "single.txt"))
	refresh(t, l, 1)
	dir := entry(t, l, 1, "folder")
	refresh(t, l, dir.ID)
	nested := entry(t, l, dir.ID, "nested")
	refresh(t, l, nested.ID)
	child := entry(t, l, nested.ID, "video.mp4")
	for _, e := range []Entry{entry(t, l, 1, "single.txt"), dir} {
		if err := l.Delete(t.Context(), e.ID, e.Version); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(root, e.Name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("target still exists: %v", err)
		}
	}
	if _, err := l.Get(t.Context(), child.ID); err == nil {
		t.Fatal("deleted descendant remains accessible")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target was touched: %v", err)
	}
	var count int
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM directory_items").Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("stale index: count=%d err=%v", count, err)
	}
}

func TestDeleteRejectsChangedAndProtectedEntries(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "video.mp4"))
	refresh(t, l, 1)
	e := entry(t, l, 1, "video.mp4")
	if err := l.Delete(t.Context(), 1, 1); err == nil {
		t.Fatal("allowed root deletion")
	}
	if err := l.Delete(t.Context(), e.ID, e.Version+1); err == nil {
		t.Fatal("allowed stale version")
	}
	l.Busy = func(context.Context, int64) bool { return true }
	if err := l.Delete(t.Context(), e.ID, e.Version); err == nil {
		t.Fatal("allowed busy deletion")
	}
	l.Busy = nil
	if err := os.WriteFile(filepath.Join(root, e.Name), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(t.Context(), e.ID, e.Version); err == nil {
		t.Fatal("allowed changed file deletion")
	}
	if _, err := os.Stat(filepath.Join(root, e.Name)); err != nil {
		t.Fatal(err)
	}
}
