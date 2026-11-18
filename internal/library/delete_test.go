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
	dir := entry(t, entryOptions{l: l, parent: 1, name: "folder"})
	refresh(t, l, dir.ID)
	nested := entry(t, entryOptions{l: l, parent: dir.ID, name: "nested"})
	refresh(t, l, nested.ID)
	child := entry(t, entryOptions{l: l, parent: nested.ID, name: "video.mp4"})
	for _, e := range []Entry{entry(t, entryOptions{l: l, parent: 1, name: "single.txt"}), dir} {
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
	e := entry(t, entryOptions{l: l, parent: 1, name: "video.mp4"})
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

func TestDeleteAfterRecursiveMarkPreservesSiblingScanAndMarks(t *testing.T) {
	l, root, _ := fixture(t)
	ctx := t.Context()
	for _, name := range []string{"edited", "sibling"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(root, name, "video.mp4"))
	}
	if result := l.SetProcessed(ctx, []int64{1}, true); result.Changed != 2 || result.Failed != 0 {
		t.Fatalf("recursive mark: %+v", result)
	}
	edited := entry(t, entryOptions{l: l, parent: 1, name: "edited"})
	sibling := entry(t, entryOptions{l: l, parent: 1, name: "sibling"})
	video := entry(t, entryOptions{l: l, parent: edited.ID, name: "video.mp4"})

	// Interleave the delete after a sibling scan has staged a page, before publication.
	state, err := l.DirectoryState(ctx, sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	gen := state.Generation + 1
	items := []stagedScanItem{{scannedDirectoryEntry: scannedDirectoryEntry{name: "new.mp4", kind: "file", identity: "1:2"}}}
	if err := l.writeScanGenerationPage(ctx, writeScanGenerationPageOptions{
		ID: sibling.ID, Gen: gen, Marker: "directory-scan:sibling", Items: items,
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(ctx, video.ID, video.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Rename(ctx, RenameRequest{ID: edited.ID, Version: edited.Version, Name: "empty", Key: "rename-empty"}); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	entries, err := l.WithProgressBatch(ctx, []Entry{edited, sibling})
	if err != nil {
		t.Fatal(err)
	}
	if got := *entries[0].Progress; got != (DirectoryProgress{}) {
		t.Errorf("renamed empty directory: %+v", got)
	}
	if got := *entries[1].Progress; got != (DirectoryProgress{Total: 1, Processed: 1}) {
		t.Errorf("sibling progress: %+v", got)
	}
	page, err := l.ListPage(ctx, ListPageOptions{ID: sibling.ID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Processed != "marked" {
		t.Fatalf("published sibling files: %+v, error: %v", page.Items, err)
	}
	var staged bool
	if err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM directory_items WHERE dir=? AND generation=? AND entry=?)",
			sibling.ID, gen, items[0].entry).Scan(&staged)
	}); err != nil || !staged {
		t.Fatalf("sibling scan page retained: %t, error: %v", staged, err)
	}
}
