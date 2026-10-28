package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestScanDirectoryDiscoversNestedDirectoriesAndChanges(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"changed.mp4", "removed.mp4", "keep.mp4"} {
		put(t, filepath.Join(root, name))
	}
	refresh(t, l, 1)
	before, err := l.DirectoryState(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	changed := entry(t, entryOptions{l: l, parent: 1, name: "changed.mp4"})
	if err := os.WriteFile(filepath.Join(root, "changed.mp4"), []byte("a different complete video fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "removed.mp4")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "added.mp4"))
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "a", "nested.mp4"))
	put(t, filepath.Join(root, "a", "b", "deep.mp4"))
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	result, err := l.ScanDirectory(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 3 || result.Added != 3 || result.Changed != 1 || result.Removed != 1 || len(result.Directories) != 1 {
		t.Fatalf("unexpected scan counts: %+v", result)
	}
	current := entry(t, entryOptions{l: l, parent: 1, name: "changed.mp4"})
	if current.ID != changed.ID || current.Version != changed.Version+1 {
		t.Fatalf("content update lost identity or version: before=%+v after=%+v", changed, current)
	}
	after, err := l.DirectoryState(t.Context(), 1)
	if err != nil || after.Generation == before.Generation || after.Error.Code != "" || after.Refreshing {
		t.Fatalf("scan was not published: %+v %v", after, err)
	}
	child, err := l.ScanDirectory(t.Context(), result.Directories[0])
	if err != nil || child.Files != 1 || len(child.Directories) != 1 {
		t.Fatalf("nested scan: %+v %v", child, err)
	}
	deep, err := l.ScanDirectory(t.Context(), child.Directories[0])
	if err != nil || deep.Files != 1 || len(deep.Directories) != 0 {
		t.Fatalf("deep scan: %+v %v", deep, err)
	}
	link := entry(t, entryOptions{l: l, parent: 1, name: "linked"})
	if _, err := l.ScanDirectory(t.Context(), link.ID); err == nil {
		t.Fatal("symlink target was scanned")
	}
}

func TestScanDirectoryUnchangedDoesNotRewriteIndex(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "unchanged.mp4"))
	refresh(t, l, 1)
	before, err := l.DirectoryState(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	file := entry(t, entryOptions{l: l, parent: 1, name: "unchanged.mp4"})
	var observation string
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT observed FROM entries WHERE id=?", file.ID).Scan(&observation)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE directory_state SET refreshed='2000-01-01T00:00:00Z';
	 CREATE TRIGGER reject_observation BEFORE UPDATE OF observed ON entries BEGIN SELECT RAISE(ABORT,'index rewrite'); END;
	 CREATE TRIGGER reject_items BEFORE INSERT ON directory_items BEGIN SELECT RAISE(ABORT,'generation rebuild'); END`)
	}); err != nil {
		t.Fatal(err)
	}
	result, err := l.ScanDirectory(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	after, err := l.DirectoryState(t.Context(), 1)
	if err != nil || after.Generation != before.Generation || after.Refreshed == "2000-01-01T00:00:00Z" || after.Error.Code != "" {
		t.Fatalf("unchanged scan state: %+v %v", after, err)
	}
	if result.Added+result.Changed+result.Removed != 0 || result.Files != 1 {
		t.Fatalf("unchanged directory reported changes: %+v", result)
	}
	var observed string
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT observed FROM entries WHERE id=?", file.ID).Scan(&observed)
	}); err != nil || observed != observation {
		t.Fatalf("unchanged entry was rewritten: %q -> %q, %v", observation, observed, err)
	}
}

func TestScanDirectorySlowMetadataDoesNotBlockRename(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	put(t, filepath.Join(root, "b.mp4"))
	refresh(t, l, 1)
	before := scanTestDirectoryState(t, l, 1)
	file := entry(t, entryOptions{l: l, parent: 1, name: "b.mp4"})
	entered, released := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	enter := sync.OnceFunc(func() { close(entered) })
	done := make(chan error, 1)
	go func() {
		_, err := l.scanDirectoryWithMetadata(t.Context(), 1,
			func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) },
			func(dir *os.File, de os.DirEntry) (scannedDirectoryEntry, error) {
				item, err := scanPinnedDirectoryEntry(dir, de)
				if de.Name() == "a.mp4" {
					enter()
					<-released
				}
				return item, err
			},
		)
		done <- err
		close(done)
	}()
	t.Cleanup(func() { release(); waitScanResult(t, done) })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata collection did not start")
	}
	state, err := l.DirectoryState(t.Context(), 1)
	if err != nil || !state.Refreshing {
		t.Fatalf("scan activity was not exposed: %+v %v", state, err)
	}
	renameDone := make(chan error, 1)
	go func() {
		_, err := l.Rename(t.Context(), RenameRequest{ID: file.ID, Version: file.Version, Name: "renamed.mp4", Key: "scan-concurrent-rename"})
		renameDone <- err
	}()
	select {
	case err := <-renameDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NAS metadata read held the global mutation lock")
	}
	release()
	if err := waitScanResult(t, done); !errors.Is(err, ErrDirectoryScanChanged) {
		t.Fatalf("concurrent rename did not invalidate scan: %v", err)
	}
	after := scanTestDirectoryState(t, l, 1)
	if after.Generation != before.Generation {
		t.Fatal("stale scan published a new generation")
	}
	current := entry(t, entryOptions{l: l, parent: 1, name: "renamed.mp4"})
	if current.ID != file.ID || current.Version != file.Version+1 {
		t.Fatalf("scan damaged a committed rename: %+v", current)
	}
}

func TestScanDirectoryIgnoresUnrelatedIndexChanges(t *testing.T) {
	for _, rebuild := range []bool{false, true} {
		for _, operation := range []struct {
			name       string
			changeFile bool
			background bool
		}{
			{name: "refresh-unchanged"},
			{name: "refresh-changed", changeFile: true},
			{name: "scan-changed", changeFile: true, background: true},
		} {
			t.Run(fmt.Sprintf("rebuild=%v/%s", rebuild, operation.name), func(t *testing.T) {
				l, root, _ := fixture(t)
				for _, name := range []string{"a", "b"} {
					if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
						t.Fatal(err)
					}
					put(t, filepath.Join(root, name, "video.mp4"))
				}
				refresh(t, l, 1)
				a, b := entry(t, entryOptions{l: l, parent: 1, name: "a"}).ID, entry(t, entryOptions{l: l, parent: 1, name: "b"}).ID
				refresh(t, l, a)
				refresh(t, l, b)
				before, err := l.DirectoryState(t.Context(), a)
				if err != nil {
					t.Fatal(err)
				}
				if rebuild {
					put(t, filepath.Join(root, "a", "added.mp4"))
				}
				var first *os.File
				changed := false
				result, err := l.scanDirectory(t.Context(), a, func(dir *os.File, n int) ([]os.DirEntry, error) {
					if first == nil {
						first = dir
					} else if dir != first && !changed {
						changed = true
						if operation.changeFile {
							if err := os.WriteFile(filepath.Join(root, "b", "video.mp4"), []byte("unrelated file changed during the scan"), 0o600); err != nil {
								return nil, err
							}
						}
						if operation.background {
							if _, err := l.ScanDirectory(t.Context(), b); err != nil {
								return nil, err
							}
						} else if err := l.Refresh(t.Context(), b); err != nil {
							return nil, err
						}
					}
					return dir.ReadDir(n)
				})
				if err != nil {
					t.Fatalf("unrelated directory update invalidated the scan: %v", err)
				}
				if !changed {
					t.Fatal("unrelated directory was not updated during the scan")
				}
				wantFiles, wantAdded := int64(1), int64(0)
				if rebuild {
					wantFiles, wantAdded = 2, 1
				}
				if result.Files != wantFiles || result.Added != wantAdded || result.Changed+result.Removed != 0 {
					t.Fatalf("unrelated changes affected scan counts: %+v", result)
				}
				after, err := l.DirectoryState(t.Context(), a)
				if err != nil || after.Error.Code != "" || after.Refreshing || (after.Generation != before.Generation) != rebuild {
					t.Fatalf("scan did not publish the expected generation: before=%+v after=%+v err=%v", before, after, err)
				}
			})
		}
	}
}

func TestScanDirectoryIgnoresUnchangedAncestorRefresh(t *testing.T) {
	l, root, _ := fixture(t)
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(inside, "video.mp4"))
	refresh(t, l, 1)
	id := entry(t, entryOptions{l: l, parent: 1, name: "inside"}).ID
	refresh(t, l, id)
	before, err := l.DirectoryState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	refreshed := false
	result, err := l.scanDirectory(t.Context(), id, func(dir *os.File, n int) ([]os.DirEntry, error) {
		if !refreshed {
			refreshed = true
			if err := l.Refresh(t.Context(), 1); err != nil {
				return nil, err
			}
		}
		return dir.ReadDir(n)
	})
	if err != nil || result.Files != 1 || result.Added+result.Changed+result.Removed != 0 {
		t.Fatalf("unchanged ancestor refresh invalidated the scan: %+v %v", result, err)
	}
	after, err := l.DirectoryState(t.Context(), id)
	if err != nil || after.Generation != before.Generation || after.Error.Code != "" {
		t.Fatalf("unchanged ancestor refresh affected the generation: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestScanDirectoryRejectsRestoredEntryVersionChange(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "video.mp4"))
	refresh(t, l, 1)
	before, err := l.DirectoryState(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	file := entry(t, entryOptions{l: l, parent: 1, name: "video.mp4"})
	disk, err := domain.StatSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	_, err = l.scanDirectory(t.Context(), 1, func(dir *os.File, n int) ([]os.DirEntry, error) {
		if !restored {
			restored = true
			op, err := l.Rename(t.Context(), RenameRequest{ID: file.ID, Version: file.Version, Name: "renamed.mp4", Key: "scan-rename-restore"})
			if err != nil {
				return nil, err
			}
			if _, err := l.Restore(t.Context(), op.ID, "scan-restore-version"); err != nil {
				return nil, err
			}
			// Restore directory metadata too, so the file's increased index
			// version is the only remaining evidence of the committed operations.
			if err := os.Chtimes(root, disk.ModTime, disk.ModTime); err != nil {
				return nil, err
			}
			current, err := domain.StatSnapshot(root)
			if err != nil {
				return nil, err
			}
			if !current.Same(disk) {
				return nil, errors.New("test did not restore the directory snapshot")
			}
		}
		return dir.ReadDir(n)
	})
	if !errors.Is(err, ErrDirectoryScanChanged) {
		t.Fatalf("restored entry's version change was accepted: %v", err)
	}
	current := entry(t, entryOptions{l: l, parent: 1, name: "video.mp4"})
	if current.ID != file.ID || current.Identity != file.Identity || current.Size != file.Size || current.ModifiedNS != file.ModifiedNS || current.Version != file.Version+2 {
		t.Fatalf("rename/restore did not preserve content and change only the version: before=%+v after=%+v", file, current)
	}
	after, err := l.DirectoryState(t.Context(), 1)
	if err != nil || after.Generation != before.Generation || after.Error.Message == "" {
		t.Fatalf("stale scan replaced the restored entry's generation: %+v %v", after, err)
	}
}

func TestScanDirectoryMetadataFailureKeepsIndexAndCleansScratch(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "old.mp4"))
	refresh(t, l, 1)
	before := scanTestDirectoryState(t, l, 1)
	for i := range 140 {
		put(t, filepath.Join(root, fmt.Sprintf("added-%03d.mp4", i)))
	}
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	wantErr := errors.New("injected NAS metadata error")
	reads := 0
	_, err := l.scanDirectoryWithMetadata(t.Context(), 1, func(dir *os.File, n int) ([]os.DirEntry, error) {
		reads++
		return dir.ReadDir(n)
	}, func(dir *os.File, de os.DirEntry) (scannedDirectoryEntry, error) {
		if reads == 2 {
			return scannedDirectoryEntry{}, wantErr
		}
		return scanPinnedDirectoryEntry(dir, de)
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("scan error: %v", err)
	}
	after := scanTestDirectoryState(t, l, 1)
	if after.Generation != before.Generation || after.Error.Message == "" {
		t.Fatalf("metadata failure published partial data: %+v", after)
	}
	assertScanIndexCounts(t, assertScanIndexCountsOptions{l: l, generation: before.Generation, published: 1, staged: 0})
	files, err := os.ReadDir(scratch)
	if err != nil || len(files) != 0 {
		t.Fatalf("scratch was not removed: %v %v", files, err)
	}
}

func TestScanDirectoryPublicationFailureRetainsGeneration(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "old.mp4"))
	refresh(t, l, 1)
	before := scanTestDirectoryState(t, l, 1)
	for i := range 140 {
		put(t, filepath.Join(root, fmt.Sprintf("new-%03d.mp4", i)))
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `CREATE TRIGGER reject_scan_publish BEFORE UPDATE OF generation ON directory_state
	 BEGIN SELECT RAISE(ABORT,'injected publication failure'); END`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ScanDirectory(t.Context(), 1); err == nil {
		t.Fatal("publication failure was ignored")
	}
	after := scanTestDirectoryState(t, l, 1)
	if after.Generation != before.Generation || after.Error.Message == "" {
		t.Fatalf("failed scan lost complete generation: %+v", after)
	}
	assertScanIndexCounts(t, assertScanIndexCountsOptions{l: l, generation: before.Generation, published: 1, staged: 0})
	var hidden int
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM entries WHERE observed LIKE 'directory-scan:%'").Scan(&hidden)
	}); err != nil || hidden != 0 {
		t.Fatalf("failed scan left invisible candidates: %d %v", hidden, err)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "DROP TRIGGER reject_scan_publish")
	}); err != nil {
		t.Fatal(err)
	}
	result, err := l.ScanDirectory(t.Context(), 1)
	if err != nil || result.Added != 140 || result.Files != 141 {
		t.Fatalf("retry did not recover: %+v %v", result, err)
	}
}

func TestScanDirectoryRejectsContentChangeBetweenPasses(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "writing.mp4"))
	refresh(t, l, 1)
	before := scanTestDirectoryState(t, l, 1)
	var first *os.File
	changed := false
	_, err := l.scanDirectory(t.Context(), 1, func(dir *os.File, n int) ([]os.DirEntry, error) {
		if first == nil {
			first = dir
		} else if dir != first && !changed {
			changed = true
			if err := os.WriteFile(filepath.Join(root, "writing.mp4"), []byte("file is still receiving additional bytes"), 0o600); err != nil {
				return nil, err
			}
		}
		return dir.ReadDir(n)
	})
	if !errors.Is(err, ErrDirectoryScanChanged) {
		t.Fatalf("content change was published: %v", err)
	}
	for _, evidence := range []string{"entry read twice", "writing.mp4", "first:", "second:"} {
		if !strings.Contains(err.Error(), evidence) {
			t.Fatalf("content change diagnostic omitted %q: %v", evidence, err)
		}
	}
	after := scanTestDirectoryState(t, l, 1)
	if after.Generation != before.Generation {
		t.Fatal("changed file snapshots published a new generation")
	}
}

func TestScanDirectorySerializesRefresh(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "video.mp4"))
	entered, released := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	enter := sync.OnceFunc(func() { close(entered) })
	done := make(chan error, 1)
	go func() {
		_, err := l.scanDirectory(t.Context(), 1, func(dir *os.File, n int) ([]os.DirEntry, error) {
			enter()
			<-released
			return dir.ReadDir(n)
		})
		done <- err
		close(done)
	}()
	t.Cleanup(func() { release(); waitScanResult(t, done) })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not acquire its directory gate")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := l.Refresh(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("legacy refresh bypassed the per-directory scan gate: %v", err)
	}
	release()
	if err := waitScanResult(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryStateReadsIndexWhenMediaUnavailable(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "video.mp4"))
	refresh(t, l, 1)
	before := scanTestDirectoryState(t, l, 1)
	if err := os.Rename(root, root+"-unmounted"); err != nil {
		t.Fatal(err)
	}
	after, err := l.DirectoryState(t.Context(), 1)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("state endpoint accessed unavailable media: %+v %v", after, err)
	}
	if _, err := l.ScanDirectory(t.Context(), 1); err == nil {
		t.Fatal("unavailable media was treated as an empty directory")
	}
	current := scanTestDirectoryState(t, l, 1)
	if current.Generation != before.Generation || current.Error.Message == "" {
		t.Fatalf("unavailable root lost its index: %+v", current)
	}
}

func TestScanDirectoryMetadataUsesPinnedDescriptorDuringSymlinkSwitch(t *testing.T) {
	l, root, _ := fixture(t)
	inside := filepath.Join(root, "inside")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, dir := range []string{inside, outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(dir, "one.mp4"))
	}
	refresh(t, l, 1)
	id := entry(t, entryOptions{l: l, parent: 1, name: "inside"}).ID
	actual, err := domain.StatSnapshot(filepath.Join(inside, "one.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := l.scanDirectoryWithMetadata(t.Context(), id,
		func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) },
		func(dir *os.File, de os.DirEntry) (scannedDirectoryEntry, error) {
			parked := inside + "-parked"
			if err := os.Rename(inside, parked); err != nil {
				return scannedDirectoryEntry{}, err
			}
			if err := os.Symlink(outside, inside); err != nil {
				return scannedDirectoryEntry{}, errors.Join(err, os.Rename(parked, inside))
			}
			item, statErr := scanPinnedDirectoryEntry(dir, de)
			removeErr := os.Remove(inside)
			restoreErr := os.Rename(parked, inside)
			return item, errors.Join(statErr, removeErr, restoreErr)
		},
	)
	if err != nil || result.Files != 1 {
		t.Fatalf("pinned metadata scan: %+v %v", result, err)
	}
	indexed := entry(t, entryOptions{l: l, parent: id, name: "one.mp4"})
	if indexed.Identity != Identity(actual) {
		t.Fatalf("scan published metadata from outside root: indexed=%s actual=%s", indexed.Identity, Identity(actual))
	}
}

func TestScanDirectoryReclaimsInterruptedCandidatesAfterRestart(t *testing.T) {
	for _, removeNew := range []bool{false, true} {
		t.Run(fmt.Sprintf("new-file-removed=%v", removeNew), func(t *testing.T) {
			l, root, _ := fixture(t)
			put(t, filepath.Join(root, "old.mp4"))
			refresh(t, l, 1)
			put(t, filepath.Join(root, "new.mp4"))
			stage, err := newScanStage(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := stage.Close(); err != nil {
					t.Error(err)
				}
			})
			baseline, err := l.captureScanBaseline(t.Context(), 1, stage)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := openScanDirectory(root, baseline.chain)
			if err != nil {
				t.Fatal(err)
			}
			err = collectScanMetadata(t.Context(), collectScanMetadataOptions{Stage: stage, Table: "snapshot", Dir: dir, Read: func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) }})
			err = errors.Join(err, dir.Close())
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.Write(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), `UPDATE snapshot SET entry=COALESCE((SELECT id FROM baseline b WHERE b.name=snapshot.name AND b.identity=snapshot.identity),0)`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := l.buildScanGeneration(t.Context(), buildScanGenerationOptions{ID: 1, Gen: baseline.generation + 1, Marker: "directory-scan:1:interrupted", Stage: stage}); err != nil {
				t.Fatal(err)
			}
			// Retain the bounded staging commits as if the process stopped before
			// publication and deferred cleanup, then recreate the Library instance.
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if removeNew {
				if err := os.Remove(filepath.Join(root, "new.mp4")); err != nil {
					t.Fatal(err)
				}
			}
			restarted, err := New(t.Context(), l.db, root, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.ScanDirectory(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			var candidates, unpublished int
			if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), `SELECT
			 (SELECT count(*) FROM entries WHERE present=0 AND observed LIKE 'directory-scan:%'),
			 (SELECT count(*) FROM directory_items WHERE generation<>(SELECT generation FROM directory_state WHERE dir=1))`).Scan(&candidates, &unpublished)
			}); err != nil {
				t.Fatal(err)
			}
			if candidates != 0 || unpublished != 0 {
				t.Fatalf("interrupted staging survived a successful scan: candidates=%d unpublished=%d", candidates, unpublished)
			}
		})
	}
}

func TestScanCleanupCannotDeletePublishedGeneration(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "visible.mp4"))
	if _, err := l.ScanDirectory(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	state, err := l.DirectoryState(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	// A failed Commit may have an uncertain outcome. Cleanup must independently
	// protect a durable published generation, even when its caller reports failure.
	stage, err := newScanStage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := l.cleanScanStaging(t.Context(), cleanScanStagingOptions{Stage: stage, ID: 1, Gen: state.Generation, Marker: "directory-scan:1:uncertain", Published: false}); err != nil {
		t.Fatal(err)
	}
	assertScanIndexCounts(t, assertScanIndexCountsOptions{l: l, generation: state.Generation, published: 1, staged: 0})
}

func TestDirectoryScannerReclaimsLegacyOrphansAcrossDirectories(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	a, b := entry(t, entryOptions{l: l, parent: 1, name: "a"}).ID, entry(t, entryOptions{l: l, parent: 1, name: "b"}).ID
	for _, parent := range []int64{a, b} {
		for n := range 140 {
			scanTestCandidate(t, scanTestCandidateOptions{l: l, parent: parent, name: fmt.Sprintf("orphan-%d.mp4", n), marker: "directory-scan:legacy"})
		}
	}
	retained := scanTestCandidate(t, scanTestCandidateOptions{l: l, parent: a, name: "retained.mp4", marker: "retained-history"})
	scanner, err := l.NewDirectoryScanner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scanner.Close(); err != nil {
			t.Error(err)
		}
	})
	var captured int
	if err := scanner.stage.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM orphan_candidates").Scan(&captured)
	}); err != nil || captured != 280 {
		t.Fatalf("legacy orphan capture: count=%d err=%v", captured, err)
	}
	for _, parent := range []int64{a, b} {
		if _, err := scanner.ScanDirectory(t.Context(), parent); err != nil {
			t.Fatal(err)
		}
		var remaining int
		if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
			return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM entries WHERE parent=? AND observed LIKE 'directory-scan:%'", parent).Scan(&remaining)
		}); err != nil || remaining != 0 {
			t.Fatalf("directory %d retained %d legacy orphans: %v", parent, remaining, err)
		}
	}
	var exists bool
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT EXISTS(SELECT 1 FROM entries WHERE id=?)", retained).Scan(&exists)
	}); err != nil || !exists {
		t.Fatalf("cleanup removed unrelated history: exists=%v err=%v", exists, err)
	}
}

func TestScanCapturedOrphansAreRecheckedBeforeDeletion(t *testing.T) {
	for _, change := range []string{"visible", "referenced", "different-marker", "different-scan-marker"} {
		t.Run(change, func(t *testing.T) {
			l, _, _ := fixture(t)
			candidate := scanTestCandidate(t, scanTestCandidateOptions{l: l, parent: 1, name: "orphan.mp4", marker: "directory-scan:legacy"})
			scanner, err := l.NewDirectoryScanner(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := scanner.Close(); err != nil {
					t.Error(err)
				}
			})
			switch change {
			case "visible":
				_, err = storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
					return tx.ExecContext(t.Context(), "UPDATE entries SET present=1 WHERE id=?", candidate)
				})
			case "referenced":
				_, err = storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
					return tx.ExecContext(t.Context(), "INSERT INTO directory_state(dir,generation) VALUES(1,7); INSERT INTO directory_items VALUES(1,7,?,'orphan.mp4','file',1,0)", candidate)
				})
			case "different-marker":
				_, err = storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
					return tx.ExecContext(t.Context(), "UPDATE entries SET observed='retained-history' WHERE id=?", candidate)
				})
			case "different-scan-marker":
				_, err = storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
					return tx.ExecContext(t.Context(), "UPDATE entries SET observed='directory-scan:new-observation' WHERE id=?", candidate)
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := l.cleanScanStaging(t.Context(), cleanScanStagingOptions{Stage: scanner.stage, ID: 1, Gen: 7, Marker: "directory-scan:current", Published: true}); err != nil {
				t.Fatal(err)
			}
			var exists bool
			if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT EXISTS(SELECT 1 FROM entries WHERE id=?)", candidate).Scan(&exists)
			}); err != nil || !exists {
				t.Fatalf("captured candidate deleted after %s: exists=%v err=%v", change, exists, err)
			}
		})
	}
}

func TestScanCleanupCandidateFailureKeepsGenerationRows(t *testing.T) {
	l, _, _ := fixture(t)
	candidate := scanTestCandidate(t, scanTestCandidateOptions{l: l, parent: 1, name: "pending.mp4", marker: "directory-scan:interrupted"})
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO directory_state(dir,generation) VALUES(1,7);
	 INSERT INTO directory_items VALUES(1,8,?,'pending.mp4','file',1,0);
	 CREATE TRIGGER reject_candidate_cleanup BEFORE DELETE ON entries
	 WHEN OLD.observed LIKE 'directory-scan:%' BEGIN SELECT RAISE(ABORT,'cleanup failure'); END`, candidate)
	}); err != nil {
		t.Fatal(err)
	}
	scanner, err := l.NewDirectoryScanner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scanner.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := l.cleanScanStaging(ctx, cleanScanStagingOptions{Stage: scanner.stage, ID: 1, Gen: 7, Marker: "directory-scan:current", Published: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cleanup: got %v, want context cancellation", err)
	}
	if err := l.cleanScanStaging(t.Context(), cleanScanStagingOptions{Stage: scanner.stage, ID: 1, Gen: 7, Marker: "directory-scan:current", Published: true}); err == nil || !strings.Contains(err.Error(), "cleanup failure") {
		t.Fatalf("failed candidate cleanup: got %v, want trigger error", err)
	}
	var candidates, items int
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM entries WHERE id=?),(SELECT count(*) FROM directory_items WHERE dir=1 AND generation=8)`, candidate).Scan(&candidates, &items)
	}); err != nil || candidates != 1 || items != 1 {
		t.Fatalf("failed candidate cleanup left orphaned state: candidates=%d items=%d err=%v", candidates, items, err)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "DROP TRIGGER reject_candidate_cleanup")
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.cleanScanStaging(t.Context(), cleanScanStagingOptions{Stage: scanner.stage, ID: 1, Gen: 7, Marker: "directory-scan:current", Published: true}); err != nil {
		t.Fatal(err)
	}
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM entries WHERE id=?),(SELECT count(*) FROM directory_items WHERE dir=1 AND generation=8)`, candidate).Scan(&candidates, &items)
	}); err != nil || candidates != 0 || items != 0 {
		t.Fatalf("cleanup retry failed: candidates=%d items=%d err=%v", candidates, items, err)
	}
}

func TestScanCleanupFindsCommittedPageAfterStageUpdateFailure(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "new.mp4"))
	stage, err := newScanStage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	baseline, err := l.captureScanBaseline(t.Context(), 1, stage)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := openScanDirectory(root, baseline.chain)
	if err != nil {
		t.Fatal(err)
	}
	err = collectScanMetadata(t.Context(), collectScanMetadataOptions{Stage: stage, Table: "snapshot", Dir: dir, Read: func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) }})
	err = errors.Join(err, dir.Close())
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "CREATE TRIGGER reject_stage_update BEFORE UPDATE OF entry ON snapshot BEGIN SELECT RAISE(ABORT,'stage failure'); END")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	const marker = "directory-scan:stage-failure"
	if err := l.buildScanGeneration(t.Context(), buildScanGenerationOptions{ID: 1, Gen: 8, Marker: marker, Stage: stage}); err == nil {
		t.Fatal("stage update failure was ignored")
	}
	if err := l.cleanScanStaging(t.Context(), cleanScanStagingOptions{Stage: stage, ID: 1, Gen: 8, Marker: marker, Published: false}); err != nil {
		t.Fatal(err)
	}
	var candidates, items int
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM entries WHERE observed=?),(SELECT count(*) FROM directory_items WHERE dir=1 AND generation=8)`, marker).Scan(&candidates, &items)
	}); err != nil || candidates != 0 || items != 0 {
		t.Fatalf("cleanup missed the committed page: candidates=%d items=%d err=%v", candidates, items, err)
	}
}

type scanTestCandidateOptions struct {
	l      *Library
	parent int64
	name   string
	marker string
}

func scanTestCandidate(t *testing.T, options scanTestCandidateOptions) int64 {
	t.Helper()
	l, parent, name, marker := options.l, options.parent, options.name, options.marker

	res, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO entries(parent,name,kind,identity,size,mtime_ns,present,observed) VALUES(?,?,'file',?,1,0,0,?)", parent, name, name, marker)
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDirectoryScannerReusesStageAfterFailureAndCancellation(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(root, name, "same-name.mp4"))
	}
	refresh(t, l, 1)
	a, b := entry(t, entryOptions{l: l, parent: 1, name: "a"}).ID, entry(t, entryOptions{l: l, parent: 1, name: "b"}).ID
	scanner, err := l.NewDirectoryScanner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scanner.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), fmt.Sprintf(`CREATE TRIGGER reject_one_scan BEFORE INSERT ON directory_state
 WHEN NEW.dir=%d BEGIN SELECT RAISE(ABORT,'injected publication failure'); END`, a))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.ScanDirectory(t.Context(), a); err == nil {
		t.Fatal("publication failure was ignored")
	}
	result, err := scanner.ScanDirectory(t.Context(), b)
	if err != nil || result.Files != 1 || result.Added != 1 || result.Changed+result.Removed != 0 {
		t.Fatalf("failed-directory evidence survived reuse: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scanner.ScanDirectory(ctx, a); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan: %v", err)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "DROP TRIGGER reject_one_scan")
	}); err != nil {
		t.Fatal(err)
	}
	result, err = scanner.ScanDirectory(t.Context(), a)
	if err != nil || result.Files != 1 || result.Added != 1 {
		t.Fatalf("cancelled-directory retry: %+v %v", result, err)
	}
	if entry(t, entryOptions{l: l, parent: a, name: "same-name.mp4"}).ID == entry(t, entryOptions{l: l, parent: b, name: "same-name.mp4"}).ID {
		t.Fatal("stage reused an entry ID from another directory")
	}
	path := scanner.stage.Path()
	if err := scanner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage not removed after close: %v", err)
	}
	if _, err := scanner.ScanDirectory(t.Context(), a); err == nil {
		t.Fatal("closed scanner accepted new work")
	}
}

func TestScanDirectoryRejectsAncestorReplacementAfterPinnedVerification(t *testing.T) {
	for _, replaceAncestor := range []bool{false, true} {
		t.Run(fmt.Sprintf("ancestor=%v", replaceAncestor), func(t *testing.T) {
			l, root, _ := fixture(t)
			inside := filepath.Join(root, "inside")
			if err := os.Mkdir(inside, 0o700); err != nil {
				t.Fatal(err)
			}
			scanned := inside
			if replaceAncestor {
				scanned = filepath.Join(inside, "nested")
				if err := os.Mkdir(scanned, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			put(t, filepath.Join(scanned, "video.mp4"))
			refresh(t, l, 1)
			id := entry(t, entryOptions{l: l, parent: 1, name: "inside"}).ID
			if replaceAncestor {
				refresh(t, l, id)
				id = entry(t, entryOptions{l: l, parent: id, name: "nested"}).ID
			}
			refresh(t, l, id)
			before := scanTestDirectoryState(t, l, id)
			outside := t.TempDir()
			put(t, filepath.Join(outside, "video.mp4"))
			var replace sync.Once
			_, err := l.scanDirectoryWithMetadata(t.Context(), id,
				func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) },
				func(dir *os.File, de os.DirEntry) (scannedDirectoryEntry, error) {
					var replaceErr error
					replace.Do(func() {
						if replaceErr = os.Rename(inside, inside+"-parked"); replaceErr == nil {
							replaceErr = os.Symlink(outside, inside)
						}
					})
					if replaceErr != nil {
						return scannedDirectoryEntry{}, replaceErr
					}
					return scanPinnedDirectoryEntry(dir, de)
				})
			if err == nil {
				t.Fatal("final ancestor verification accepted a replaced path")
			}
			after := scanTestDirectoryState(t, l, id)
			if after.Generation != before.Generation || after.Error.Message == "" {
				t.Fatalf("ancestor replacement lost the old generation: %+v", after)
			}
		})
	}
}

func waitScanResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not finish")
		return nil
	}
}

type assertScanIndexCountsOptions struct {
	l          *Library
	generation int64
	published  int
	staged     int
}

func assertScanIndexCounts(t *testing.T, options assertScanIndexCountsOptions) {
	t.Helper()
	l, generation, published, staged := options.l, options.generation, options.published, options.staged

	var visible, indexed, unpublished int
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT
	 (SELECT count(*) FROM entries WHERE parent=1 AND present=1),
	 (SELECT count(*) FROM directory_items WHERE dir=1 AND generation=?),
	 (SELECT count(*) FROM directory_items WHERE dir=1 AND generation<>?)`, generation, generation).Scan(&visible, &indexed, &unpublished)
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal([]int{visible, indexed, unpublished}, []int{published, published, staged}) {
		t.Fatalf("index counts: visible=%d published=%d unpublished=%d", visible, indexed, unpublished)
	}
}

func scanTestDirectoryState(t *testing.T, l *Library, id int64) DirectoryState {
	t.Helper()
	state, err := l.DirectoryState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
