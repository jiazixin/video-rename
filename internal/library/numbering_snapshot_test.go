package library

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestNumberingCancelledWhileWaitingForLock(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"restore", "recovery"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			l, _, batch := numberedFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var workers sync.WaitGroup
			defer workers.Wait()
			l.mu.Lock()
			defer l.mu.Unlock()
			result := make(chan error, 1)
			workers.Go(func() {
				var err error
				if operation == "restore" {
					_, err = l.PreviewNumberingRestore(ctx, batch.ID)
				} else {
					_, err = l.RecoverNumbering(ctx, batch.ID)
				}
				result <- err
			})
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("got %v, want context.Canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled numbering still waits for the file operation lock")
			}
		})
	}
}

func TestNumberingDirectoryCancellation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for i := range 2*refreshPageSize + 1 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%04d.jpg", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, stop := range []int{1, refreshPageSize, refreshPageSize + 1} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			visited := 0
			err := scanNumberingDirectory(ctx, root, func(os.DirEntry) error {
				visited++
				if visited == stop {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || visited != stop {
				t.Fatalf("visited %d entries, error %v; want %d and context.Canceled", visited, err, stop)
			}
			snapshot, err := directoryFingerprint(ctx, root)
			if !errors.Is(err, context.Canceled) || snapshot != "" {
				t.Fatalf("cancelled fingerprint returned %q, %v", snapshot, err)
			}
		})
	}
}

func TestNumberingSnapshotIncludesEveryPageInNameOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var names []string
	for i := 2 * refreshPageSize; i >= 0; i-- {
		name := fmt.Sprintf("%04d.jpg", i)
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	slices.Reverse(names)
	raw, err := directoryFingerprint(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := decodeNumberingSnapshot(raw)
	if !ok || len(snapshot.Entries) != len(names) {
		t.Fatalf("invalid or incomplete snapshot: %d entries", len(snapshot.Entries))
	}
	for i, entry := range snapshot.Entries {
		if entry.Name != names[i] {
			t.Fatalf("entry %d: got %q, want %q", i, entry.Name, names[i])
		}
	}
}

func TestNumberingSnapshotDiagnosesNonParticipatingEntries(t *testing.T) {
	l, root, b := numberedFixture(t)
	original := filepath.Join(root, "video.MP4")
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := directoryFingerprint(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	// Diagnostics must use the failing observation even if the files change again.
	if err := os.Remove(filepath.Join(root, "added.txt")); err != nil {
		t.Fatal(err)
	}
	persisted, err := l.Numbering(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var detail strings.Builder
	if !describeNumberingSnapshotDiff(&detail, persisted.Fingerprint, actual) {
		t.Fatal("snapshot not persisted")
	}
	for _, want := range []string{"video.MP4", "missing from the current listing", "added.txt", "new entry", "2 entries differ"} {
		if !strings.Contains(detail.String(), want) {
			t.Fatal(want, detail.String())
		}
	}
}

func TestNumberingRejectsInvalidPreviewFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"plain", "fingerprint"},
		{"malformed", "{"},
		{"unsupported_version", `{"version":2,"hash":"fingerprint","entries":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, root, b := numberedFixture(t)
			before, err := directoryFingerprint(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(t.Context(), "UPDATE image_numberings SET fingerprint=? WHERE id=?", tc.raw, b.ID)
			}); err != nil {
				t.Fatal(err)
			}
			_, err = l.ApplyNumbering(t.Context(), b.ID, nil)
			diagnostic, ok := errors.AsType[*domain.JobError](err)
			if !ok || diagnostic.Code != domain.CodeSourceChanged || diagnostic.Details["reason"] != "preview_invalid" {
				t.Fatal("invalid preview accepted or misdiagnosed", err)
			}
			after, err := directoryFingerprint(t.Context(), root)
			if err != nil || after != before {
				t.Fatal("invalid preview changed files", err)
			}
			saved, err := l.Numbering(t.Context(), b.ID)
			if err != nil || saved.Status != "preview" || saved.Fingerprint != tc.raw {
				t.Fatal("invalid preview was rewritten", saved, err)
			}
			var operations int
			if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM operations").Scan(&operations)
			}); err != nil || operations != 0 {
				t.Fatal("invalid preview created file operations", operations, err)
			}
		})
	}
}
func TestNumberingSnapshotReportsIdentityOnlyDrift(t *testing.T) {
	before := numberingDirectorySnapshot{Version: 4, Hash: "old", Entries: []numberingEntrySnapshot{{Name: "photo.jpg", Type: "----------", Identity: "1:100", Size: 42, ModifiedNS: 1790467200000000000}}}
	old, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	before.Hash = "new"
	before.Entries[0].Identity = "1:999"
	current, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var detail strings.Builder
	if !describeNumberingSnapshotDiff(&detail, string(old), string(current)) {
		t.Fatal("not decoded")
	}
	for _, want := range []string{"photo.jpg", "1:100 -> 1:999", "size 42 -> 42", "1 entries differ"} {
		if !strings.Contains(detail.String(), want) {
			t.Fatal(want, detail.String())
		}
	}
}
