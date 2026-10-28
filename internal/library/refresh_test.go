package library

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

type refreshTestEntry struct {
	os.DirEntry
	info func() (os.FileInfo, error)
}

func TestRefreshMetadataFailureKeepsCompleteGeneration(t *testing.T) {
	for _, cancelScan := range []bool{false, true} {
		name := "read error"
		if cancelScan {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			l, root, _ := fixture(t)
			put(t, filepath.Join(root, "old.mp4"))
			refresh(t, l, 1)
			var generation int64
			if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT generation FROM directory_state WHERE dir=1").Scan(&generation)
			}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
				put(t, filepath.Join(root, name))
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantErr := errors.New("metadata unavailable")
			if cancelScan {
				wantErr = context.Canceled
			}
			broken := refreshTestEntry{DirEntry: entries[2], info: func() (os.FileInfo, error) {
				if cancelScan {
					cancel()
					return entries[2].Info()
				}
				return nil, wantErr
			}}
			calls := 0
			err = l.refreshDirectory(ctx, 1, func(n int) ([]os.DirEntry, error) {
				if n != 128 {
					t.Errorf("unbounded read: %d", n)
				}
				calls++
				if calls == 1 {
					return entries[:1], nil
				}
				return []os.DirEntry{entries[1], broken}, io.EOF
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("refresh error = %v, want %v", err, wantErr)
			}
			var published int64
			var scanError string
			if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT generation,error FROM directory_state WHERE dir=1").Scan(&published, &scanError)
			}); err != nil {
				t.Fatal(err)
			}
			if published != generation || scanError == "" {
				t.Fatalf("failed scan published: generation %d -> %d, error %q", generation, published, scanError)
			}
			for name, want := range map[string]int{"old.mp4": 1, "a.mp4": 0, "b.mp4": 0, "c.mp4": 0} {
				var count int
				if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
					return tx.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries WHERE parent=1 AND name=? AND present=1", name).Scan(&count)
				}); err != nil {
					t.Fatal(err)
				}
				if count != want {
					t.Errorf("%s present = %d, want %d", name, count, want)
				}
			}
			// A normal retry replaces the old generation and includes all entries.
			refresh(t, l, 1)
			for _, name := range []string{"old.mp4", "a.mp4", "b.mp4", "c.mp4"} {
				entry(t, l, 1, name)
			}
		})
	}
}

func (e refreshTestEntry) Info() (os.FileInfo, error) { return e.info() }

func TestRefreshEmptyDirectoryKeepsCompleteGeneration(t *testing.T) {
	l, _, _ := fixture(t)
	refresh(t, l, 1)
	before, err := l.DirectoryState(t.Context(), 1)
	if err != nil || before.Generation == 0 {
		t.Fatalf("empty directory was not published: %+v %v", before, err)
	}
	refresh(t, l, 1)
	after, err := l.DirectoryState(t.Context(), 1)
	if err != nil || after.Generation != before.Generation || after.Error.Message != "" {
		t.Fatalf("unchanged empty directory received a new generation: before=%+v after=%+v error=%v", before, after, err)
	}
}

func TestRefreshMetadataChangesPublishNewGeneration(t *testing.T) {
	for _, change := range []string{"size", "mtime_ns"} {
		t.Run(change, func(t *testing.T) {
			l, root, _ := fixture(t)
			path := filepath.Join(root, "a.mp4")
			put(t, path)
			refresh(t, l, 1)
			before, err := l.DirectoryState(t.Context(), 1)
			if err != nil {
				t.Fatal(err)
			}
			old := entry(t, l, 1, "a.mp4")
			if change == "size" {
				if err := os.WriteFile(path, []byte("changed video fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				at := time.Unix(0, old.ModifiedNS)
				if err := os.Chtimes(path, at.Add(time.Second), at.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			refresh(t, l, 1)
			after, err := l.DirectoryState(t.Context(), 1)
			if err != nil || after.Generation == before.Generation {
				t.Fatalf("%s change retained old generation: before=%+v after=%+v error=%v", change, before, after, err)
			}
			current := entry(t, l, 1, old.Name)
			if current.ID != old.ID || current.Identity != old.Identity || current.Version != old.Version+1 {
				t.Fatalf("content change lost identity or version: before=%+v after=%+v", old, current)
			}
			if _, err := l.ListOrdered(t.Context(), ListOrderedOptions{ID: 1, Offset: 0, Limit: 50, Generation: before.Generation, Query: "", Kind: "", Status: "", Force: false, SortBy: "name_asc"}); err == nil {
				t.Fatal("metadata change accepted an old-generation cursor")
			}
		})
	}
}

func TestRefreshRetryPreservesEntriesUntilCompleteObservation(t *testing.T) {
	l, root, _ := fixture(t)
	path := filepath.Join(root, "a.mp4")
	put(t, path)
	put(t, filepath.Join(root, "b.mp4"))
	refresh(t, l, 1)
	before, err := l.DirectoryState(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	old := entry(t, l, 1, "a.mp4")
	if err := os.WriteFile(path, []byte("changed video fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("directory read unavailable")
	calls := 0
	err = l.refreshDirectory(t.Context(), 1, func(int) ([]os.DirEntry, error) {
		calls++
		if calls == 1 {
			return entries[:1], nil
		}
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("partial refresh returned %v, want %v", err, wantErr)
	}
	partial, err := l.DirectoryState(t.Context(), 1)
	if err != nil || partial.Generation != before.Generation || partial.Error.Message == "" {
		t.Fatalf("partial refresh lost published state: %+v %v", partial, err)
	}
	updated := entry(t, l, 1, old.Name)
	if updated.Size != old.Size || updated.Version != old.Version {
		t.Fatalf("partial refresh changed published entry metadata: before=%+v after=%+v", old, updated)
	}
	refresh(t, l, 1)
	after, err := l.DirectoryState(t.Context(), 1)
	if err != nil || after.Generation == before.Generation || after.Error.Message != "" {
		t.Fatalf("retry mistook live entries for the published generation: before=%+v after=%+v error=%v", before, after, err)
	}
	current := entry(t, l, 1, old.Name)
	if current.Version != updated.Version+1 {
		t.Fatalf("retry failed to publish the changed entry version: before=%+v after=%+v", updated, current)
	}
}

func TestRefreshSlowMetadataLeavesDatabaseAvailable(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	signalEntered := sync.OnceFunc(func() { close(entered) })
	slow := refreshTestEntry{DirEntry: entries[0], info: func() (os.FileInfo, error) {
		signalEntered()
		<-release
		return entries[0].Info()
	}}
	go func() {
		done <- l.refreshDirectory(t.Context(), 1, func(int) ([]os.DirEntry, error) {
			return []os.DirEntry{slow}, io.EOF
		})
	}()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Errorf("refresh: %v", err)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach metadata read")
	}
	if !l.mu.TryLock() {
		t.Fatal("filesystem metadata holds the global mutation lock")
	}
	l.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var count int
	if err := l.db.View(ctx, func(tx *sql.Tx) error { return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entries").Scan(&count) }); err != nil {
		t.Fatalf("database read blocked by filesystem metadata: %v", err)
	}
	if _, err := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE entries SET observed=observed WHERE id=1")
	}); err != nil {
		t.Fatalf("database write blocked by filesystem metadata: %v", err)
	}
}

func (l *Library) refreshDirectory(ctx context.Context, id int64, read func(int) ([]os.DirEntry, error)) error {
	first := true
	_, err := l.scanDirectoryWithMetadata(ctx, id, func(dir *os.File, n int) ([]os.DirEntry, error) {
		if !first {
			return dir.ReadDir(n)
		}
		batch, err := read(n)
		if errors.Is(err, io.EOF) {
			first = false
		}
		return batch, err
	}, func(_ *os.File, de os.DirEntry) (scannedDirectoryEntry, error) {
		info, err := de.Info()
		if err != nil {
			return scannedDirectoryEntry{}, err
		}
		snap, err := domain.SnapshotFromInfo(info)
		if err != nil {
			return scannedDirectoryEntry{}, err
		}
		return scannedDirectoryEntry{de.Name(), kindOf(info), Identity(snap), stamp(snap), snap.Size}, nil
	})
	return err
}
