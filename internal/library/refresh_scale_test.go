//go:build scale

package library

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Deliberately delayed metadata calls model latency, not actual SMB performance.
func TestScaleRefreshDelayedMetadata(t *testing.T) {
	const totalFiles = 512
	const metadataDelay = 2 * time.Millisecond
	l, root, _ := fixture(t)
	for f := range totalFiles {
		put(t, filepath.Join(root, fmt.Sprintf("clip-%05d.mp4", f)))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls, active, peak atomic.Int64
	delayed := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		delayed = append(delayed, refreshTestEntry{DirEntry: entry, info: func() (os.FileInfo, error) {
			calls.Add(1)
			current := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); current > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, current) {
					break
				}
			}
			time.Sleep(metadataDelay)
			return entry.Info()
		}})
	}
	offset := 0
	readDir := func(n int) ([]os.DirEntry, error) {
		if n != 128 {
			t.Errorf("unbounded directory read: %d", n)
		}
		end := min(offset+n, len(delayed))
		batch := delayed[offset:end]
		offset = end
		if end == len(delayed) {
			return batch, io.EOF
		}
		return batch, nil
	}
	start := time.Now()
	err = l.refreshDirectory(t.Context(), 1, readDir)
	elapsed := time.Since(start)
	t.Logf("%d files, Info delay=%s, elapsed=%s, Info calls=%d, peak active=%d", totalFiles, metadataDelay, elapsed, calls.Load(), peak.Load())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != totalFiles || active.Load() != 0 {
		t.Fatalf("metadata calls=%d, active=%d", calls.Load(), active.Load())
	}
	for _, query := range []string{
		"SELECT count(*) FROM entries WHERE parent=1 AND present=1",
		`SELECT count(*) FROM directory_items i
			JOIN directory_state s ON s.dir=i.dir AND s.generation=i.generation
			WHERE i.dir=1`,
	} {
		var count int
		if err := l.db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), query).Scan(&count) }); err != nil {
			t.Fatal(err)
		}
		if count != totalFiles {
			t.Fatalf("indexed/published entries=%d, want %d", count, totalFiles)
		}
	}
}
