package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startTestDirectoryRefresh(t *testing.T, l *Library, ctx context.Context, batch []os.DirEntry) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- l.refreshDirectory(ctx, 1, func(n int) ([]os.DirEntry, error) {
			if n != 128 {
				t.Errorf("metadata page size = %d, want 128", n)
			}
			return batch, io.EOF
		})
		close(done)
	}()
	return done
}

func waitTestDirectoryRefresh(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("directory refresh did not finish")
		return nil
	}
}

func TestRefreshParallelMetadataBoundedAndOrdered(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 128 {
		put(t, filepath.Join(root, fmt.Sprintf("%03d.mp4", i)))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan int, len(entries))
	finished := make(chan int, len(entries))
	others, first := make(chan struct{}), make(chan struct{})
	releaseOthers := sync.OnceFunc(func() { close(others) })
	releaseFirst := sync.OnceFunc(func() { close(first) })
	var active, peak, calls atomic.Int64
	batch := make([]os.DirEntry, len(entries))
	for i, de := range entries {
		batch[i] = refreshTestEntry{DirEntry: de, info: func() (os.FileInfo, error) {
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); n > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, n) {
					break
				}
			}
			entered <- i
			if i == 0 {
				<-first
			} else {
				<-others
			}
			info, err := de.Info()
			finished <- i
			return info, err
		}}
	}
	done := startTestDirectoryRefresh(t, l, t.Context(), batch)
	t.Cleanup(func() {
		releaseOthers()
		releaseFirst()
		waitTestDirectoryRefresh(t, done)
	})
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("metadata reads did not reach four concurrent workers")
		}
	}
	releaseOthers()
	// The first input stays blocked while every other input completes out of order.
	for range len(entries) - 1 {
		select {
		case i := <-finished:
			if i == 0 {
				t.Fatal("first metadata read completed before release")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("other metadata workers could not finish while the first was blocked")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var indexed int
	if err := l.DB.QueryRowContext(ctx, "SELECT count(*) FROM entries WHERE parent=1").Scan(&indexed); err != nil {
		t.Fatalf("metadata collection held the database connection: %v", err)
	}
	if indexed != 0 {
		t.Fatalf("incomplete metadata page inserted %d entries", indexed)
	}
	if _, err := l.DB.ExecContext(ctx, "UPDATE entries SET observed=observed WHERE id=1"); err != nil {
		t.Fatalf("metadata collection blocked database writes: %v", err)
	}
	releaseFirst()
	if err := waitTestDirectoryRefresh(t, done); err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 4 || calls.Load() != int64(len(entries)) || active.Load() != 0 {
		t.Fatalf("unbounded or incomplete reads: peak=%d calls=%d active=%d", peak.Load(), calls.Load(), active.Load())
	}
	rows, err := l.DB.Read().QueryContext(t.Context(), "SELECT name FROM entries WHERE parent=1 AND present=1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if count >= len(entries) || name != entries[count].Name() {
			t.Fatalf("parallel scan changed insertion order at %d: %q", count, name)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(entries) {
		t.Fatalf("inserted %d of %d entries", count, len(entries))
	}
}

func TestRefreshParallelCancellationJoinsMetadataWorkers(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "old.mp4"))
	refresh(t, l, 1)
	var generation int64
	if err := l.DB.QueryRow("SELECT generation FROM directory_state WHERE dir=1").Scan(&generation); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		put(t, filepath.Join(root, fmt.Sprintf("new-%02d.mp4", i)))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	entered, gate := make(chan struct{}, len(entries)), make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	var calls, active atomic.Int64
	batch := make([]os.DirEntry, len(entries))
	for i, de := range entries {
		batch[i] = refreshTestEntry{DirEntry: de, info: func() (os.FileInfo, error) {
			calls.Add(1)
			active.Add(1)
			defer active.Add(-1)
			entered <- struct{}{}
			<-gate
			return de.Info()
		}}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startTestDirectoryRefresh(t, l, ctx, batch)
	t.Cleanup(func() {
		cancel()
		release()
		waitTestDirectoryRefresh(t, done)
	})
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("metadata workers did not enter Info")
		}
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("refresh returned before in-flight metadata exited: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err := waitTestDirectoryRefresh(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh error=%v, want cancellation", err)
	}
	if calls.Load() != 4 || active.Load() != 0 {
		t.Fatalf("cancelled scan started later reads or leaked workers: calls=%d active=%d", calls.Load(), active.Load())
	}
	assertFailedRefreshPreservedGeneration(t, l, generation, 1)
}

func TestRefreshParallelMetadataErrorDiscardsWholePage(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "old.mp4"))
	refresh(t, l, 1)
	var generation int64
	if err := l.DB.QueryRow("SELECT generation FROM directory_state WHERE dir=1").Scan(&generation); err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		put(t, filepath.Join(root, fmt.Sprintf("new-%02d.mp4", i)))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected metadata error")
	entries[len(entries)-1] = refreshTestEntry{DirEntry: entries[len(entries)-1], info: func() (os.FileInfo, error) {
		return nil, wantErr
	}}
	done := startTestDirectoryRefresh(t, l, t.Context(), entries)
	if err := waitTestDirectoryRefresh(t, done); !errors.Is(err, wantErr) {
		t.Fatalf("refresh error=%v, want metadata failure", err)
	}
	assertFailedRefreshPreservedGeneration(t, l, generation, 1)
}

func TestRefreshParallelMetadataReportsEarlierInputError(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 8 {
		put(t, filepath.Join(root, fmt.Sprintf("%02d.mp4", i)))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	earlierErr, laterErr := errors.New("earlier input failed"), errors.New("later input failed first")
	entered := make(chan int, len(entries))
	first, second, others := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(first) })
	releaseSecond := sync.OnceFunc(func() { close(second) })
	releaseOthers := sync.OnceFunc(func() { close(others) })
	secondFinished := make(chan struct{})
	var calls, active atomic.Int64
	batch := make([]os.DirEntry, len(entries))
	for i, de := range entries {
		batch[i] = refreshTestEntry{DirEntry: de, info: func() (os.FileInfo, error) {
			calls.Add(1)
			active.Add(1)
			defer active.Add(-1)
			entered <- i
			switch i {
			case 0:
				<-first
				return nil, earlierErr
			case 1:
				<-second
				close(secondFinished)
				return nil, laterErr
			default:
				<-others
				return de.Info()
			}
		}}
	}
	done := startTestDirectoryRefresh(t, l, t.Context(), batch)
	t.Cleanup(func() {
		releaseFirst()
		releaseSecond()
		releaseOthers()
		waitTestDirectoryRefresh(t, done)
	})
	for range 4 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("metadata workers did not enter the first four inputs")
		}
	}
	releaseSecond()
	select {
	case <-secondFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("later error did not finish first")
	}
	select {
	case err := <-done:
		t.Fatalf("later input failure returned before joining earlier metadata: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()
	releaseOthers()
	if err := waitTestDirectoryRefresh(t, done); !errors.Is(err, earlierErr) {
		t.Fatalf("refresh error=%v, want earlier input failure %v", err, earlierErr)
	}
	if calls.Load() != 4 || active.Load() != 0 {
		t.Fatalf("failed scan started later inputs or leaked workers: calls=%d active=%d", calls.Load(), active.Load())
	}
	var count int
	if err := l.DB.QueryRow("SELECT count(*) FROM entries WHERE parent=1").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed metadata page wrote entries: count=%d error=%v", count, err)
	}
}

func TestRefreshIndexPageFailureRollsBackEntriesAndItems(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "00-old.mp4"))
	refresh(t, l, 1)
	old := entry(t, l, 1, "00-old.mp4")
	var generation int64
	if err := l.DB.QueryRow("SELECT generation FROM directory_state WHERE dir=1").Scan(&generation); err != nil {
		t.Fatal(err)
	}
	const oldObservation = "2000-01-01T00:00:00Z"
	if _, err := l.DB.ExecContext(t.Context(), "UPDATE entries SET observed=? WHERE id=?", oldObservation, old.ID); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		put(t, filepath.Join(root, fmt.Sprintf("new-%02d.mp4", i)))
	}
	trigger := `CREATE TRIGGER reject_index_item BEFORE INSERT ON directory_items
WHEN NEW.dir=1 AND NEW.name='new-02.mp4' BEGIN SELECT RAISE(ABORT,'injected directory item failure'); END`
	if _, err := l.DB.ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}
	if err := l.Refresh(t.Context(), 1); err == nil {
		t.Fatal("injected directory item insertion failure was ignored")
	}
	assertFailedRefreshPreservedGeneration(t, l, generation, 1)
	current := entry(t, l, 1, old.Name)
	var observed string
	if err := l.DB.QueryRow("SELECT observed FROM entries WHERE id=?", old.ID).Scan(&observed); err != nil {
		t.Fatal(err)
	}
	if current.ID != old.ID || current.Version != old.Version || observed != oldObservation {
		t.Fatalf("failed index page retained an entry update: before=%+v after=%+v observed=%q", old, current, observed)
	}
	if _, err := l.DB.ExecContext(t.Context(), "DROP TRIGGER reject_index_item"); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	page, err := l.ListOrdered(t.Context(), 1, 0, 50, 0, "", "", "", false, "name_asc")
	if err != nil || len(page.Items) != 5 || page.Error != "" || page.Generation == generation {
		t.Fatalf("retry did not publish rolled-back page: %+v %v", page, err)
	}
}

func assertFailedRefreshPreservedGeneration(t *testing.T, l *Library, generation int64, indexed int) {
	t.Helper()
	var published int64
	var scanError string
	if err := l.DB.QueryRow("SELECT generation,error FROM directory_state WHERE dir=1").Scan(&published, &scanError); err != nil {
		t.Fatal(err)
	}
	if published != generation || scanError == "" {
		t.Fatalf("failed scan published a generation: %d -> %d, error=%q", generation, published, scanError)
	}
	var entries, unpublished int
	if err := l.DB.QueryRow("SELECT count(*) FROM entries WHERE parent=1 AND present=1").Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if err := l.DB.QueryRow("SELECT count(*) FROM directory_items WHERE dir=1 AND generation<>?", generation).Scan(&unpublished); err != nil {
		t.Fatal(err)
	}
	if entries != indexed || unpublished != 0 {
		t.Fatalf("failed metadata page wrote partial state: entries=%d unpublished=%d", entries, unpublished)
	}
}

func TestRefreshUnchangedEntriesKeepObservationAndVersion(t *testing.T) {
	l, root, _ := fixture(t)
	names := []string{"video.mp4", "photo.jpg", "audio.mp3"}
	for _, name := range names {
		put(t, filepath.Join(root, name))
	}
	refresh(t, l, 1)
	state, err := l.DirectoryState(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	before := make([]Entry, 0, len(names))
	for _, name := range names {
		before = append(before, entry(t, l, 1, name))
	}
	const oldObservation = "2000-01-01T00:00:00Z"
	if _, err := l.DB.Exec("UPDATE entries SET observed=? WHERE parent=1", oldObservation); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DB.Exec("UPDATE directory_state SET refreshed=?,error='previous scan failed' WHERE dir=1", oldObservation); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	after, err := l.DirectoryState(t.Context(), 1)
	if err != nil || after.Generation != state.Generation || after.Refreshed == oldObservation || after.Error != "" {
		t.Fatalf("unchanged refresh changed the generation or retained stale status: before=%+v after=%+v error=%v", state, after, err)
	}
	var unpublished int
	if err := l.DB.QueryRow("SELECT count(*) FROM directory_items WHERE dir=1 AND generation<>?", state.Generation).Scan(&unpublished); err != nil || unpublished != 0 {
		t.Fatalf("unchanged refresh left staging items: %d %v", unpublished, err)
	}
	for _, old := range before {
		current := entry(t, l, 1, old.Name)
		if current.ID != old.ID || current.Version != old.Version || current.Identity != old.Identity || current.Kind != old.Kind || current.Size != old.Size || current.Mtime != old.Mtime {
			t.Fatalf("unchanged file received a new snapshot/version: before=%+v after=%+v", old, current)
		}
		var observed string
		if err := l.DB.QueryRow("SELECT observed FROM entries WHERE id=?", old.ID).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		if observed != oldObservation {
			t.Fatalf("unchanged %s received a redundant metadata write", old.Name)
		}
	}
}

func TestRefreshReplacementAllocatesEntryAndRejectsOldGeneration(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	put(t, filepath.Join(root, "keep.mp4"))
	refresh(t, l, 1)
	old := entry(t, l, 1, "a.mp4")
	page, err := l.ListOrdered(t.Context(), 1, 0, 50, 0, "", "", "", false, "name_asc")
	if err != nil {
		t.Fatal(err)
	}
	// Keep the old inode alive so replacing the path cannot reuse its identity.
	if err := os.Rename(filepath.Join(root, "a.mp4"), filepath.Join(t.TempDir(), "old.mp4")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	current := entry(t, l, 1, "a.mp4")
	if current.ID == old.ID || current.Identity == old.Identity {
		t.Fatalf("replacement reused the old indexed identity: before=%+v after=%+v", old, current)
	}
	var present int
	if err := l.DB.QueryRow("SELECT present FROM entries WHERE id=?", old.ID).Scan(&present); err != nil || present != 0 {
		t.Fatalf("replaced entry remains present: %d %v", present, err)
	}
	if _, err := l.ListOrdered(t.Context(), 1, 1, 50, page.Generation, "", "", "", false, "name_asc"); err == nil {
		t.Fatal("old generation accepted after replacement scan")
	}
}

func TestRefreshPublishFailureRollsBackAndRecordsError(t *testing.T) {
	for _, stage := range []string{"generation"} {
		t.Run(stage, func(t *testing.T) {
			l, root, _ := fixture(t)
			put(t, filepath.Join(root, "old.mp4"))
			refresh(t, l, 1)
			old := entry(t, l, 1, "old.mp4")
			var generation int64
			if err := l.DB.QueryRow("SELECT generation FROM directory_state WHERE dir=1").Scan(&generation); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(root, "new.mp4"))
			if err := os.Remove(filepath.Join(root, "old.mp4")); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER reject_publish BEFORE UPDATE OF generation ON directory_state
WHEN OLD.dir=1 BEGIN SELECT RAISE(ABORT,'injected generation publication failure'); END`
			if _, err := l.DB.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			if err := l.Refresh(t.Context(), 1); err == nil {
				t.Fatal("injected publication failure was ignored")
			}
			var published int64
			var scanError string
			if err := l.DB.QueryRow("SELECT generation,error FROM directory_state WHERE dir=1").Scan(&published, &scanError); err != nil {
				t.Fatal(err)
			}
			if published != generation || scanError == "" {
				t.Fatalf("failed publication lost old generation/error: %d -> %d, error=%q", generation, published, scanError)
			}
			var present, oldItems int
			if err := l.DB.QueryRow("SELECT present FROM entries WHERE id=?", old.ID).Scan(&present); err != nil {
				t.Fatal(err)
			}
			if err := l.DB.QueryRow("SELECT count(*) FROM directory_items WHERE dir=1 AND generation=?", generation).Scan(&oldItems); err != nil {
				t.Fatal(err)
			}
			if present != 1 || oldItems != 1 {
				t.Fatalf("failed publication kept partial mutations: old present=%d old items=%d", present, oldItems)
			}
			page, err := l.ListOrdered(t.Context(), 1, 0, 50, generation, "", "", "", false, "name_asc")
			if err != nil || len(page.Items) != 1 || page.Items[0].Name != "old.mp4" || page.Error == "" {
				t.Fatalf("previous complete generation is unavailable: %+v %v", page, err)
			}
			if _, err := l.DB.ExecContext(t.Context(), "DROP TRIGGER reject_publish"); err != nil {
				t.Fatal(err)
			}
			refresh(t, l, 1)
			page, err = l.ListOrdered(t.Context(), 1, 0, 50, 0, "", "", "", false, "name_asc")
			if err != nil || page.Generation == generation || page.Error != "" || len(page.Items) != 1 || page.Items[0].Name != "new.mp4" {
				t.Fatalf("retry did not publish complete replacement generation: %+v %v", page, err)
			}
		})
	}
}
