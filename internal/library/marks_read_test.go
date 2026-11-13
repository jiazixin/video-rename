package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/domain"
)

func TestProcessedMarksBatchSnapshotRejectsInvalidAncestors(t *testing.T) {
	for _, change := range []string{"missing", "cycle", "replacement", "symlink"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			l, root, _ := fixture(t)
			dir := filepath.Join(root, "parent")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(root, "good.mp4"))
			put(t, filepath.Join(dir, "a.mp4"))
			put(t, filepath.Join(dir, "b.mp4"))
			refresh(t, l, 1)
			parent := entry(t, entryOptions{l: l, parent: 1, name: "parent"})
			good := entry(t, entryOptions{l: l, parent: 1, name: "good.mp4"})
			refresh(t, l, parent.ID)
			a := entry(t, entryOptions{l: l, parent: parent.ID, name: "a.mp4"})
			b := entry(t, entryOptions{l: l, parent: parent.ID, name: "b.mp4"})
			switch change {
			case "missing", "cycle":
				if err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
					query := "UPDATE entries SET present=0 WHERE id=?"
					if change == "cycle" {
						query = "UPDATE entries SET parent=id WHERE id=?"
					}
					_, err := tx.ExecContext(t.Context(), query, parent.ID)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			case "replacement", "symlink":
				if err := os.Rename(dir, dir+"-original"); err != nil {
					t.Fatal(err)
				}
				if change == "symlink" {
					if err := os.Symlink(dir+"-original", dir); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					// Same leaf identities cannot legitimize a replaced ancestor.
					for _, name := range []string{"a.mp4", "b.mp4"} {
						if err := os.Link(filepath.Join(dir+"-original", name), filepath.Join(dir, name)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			result := l.SetProcessed(t.Context(), []int64{a.ID, good.ID, b.ID}, true)
			if result.Changed != 1 || result.Failed != 2 || result.Interrupted {
				t.Fatalf("%s: got %+v, want only the unrelated file marked", change, result)
			}
			assertDurableMarkProgress(t, l, result)
		})
	}
}

func TestProcessedMarksBatchSnapshotReloadsRenamedAncestor(t *testing.T) {
	l, root, _ := fixture(t)
	dir := filepath.Join(root, "parent")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for f := range markBatchSize + 1 {
		put(t, filepath.Join(dir, fmt.Sprintf("%03d.mp4", f)))
	}
	refresh(t, l, 1)
	parent := entry(t, entryOptions{l: l, parent: 1, name: "parent"})
	refresh(t, l, parent.ID)
	ids := make([]int64, 0, markBatchSize+1)
	for f := range markBatchSize + 1 {
		ids = append(ids, entry(t, entryOptions{l: l, parent: parent.ID, name: fmt.Sprintf("%03d.mp4", f)}).ID)
	}
	changed := false
	result := l.SetProcessedWithProgress(t.Context(), ids, true, func(result MarkResult) {
		if result.Changed != markBatchSize || changed {
			return
		}
		changed = true
		if err := os.Rename(dir, filepath.Join(root, "renamed")); err != nil {
			t.Fatal(err)
		}
		if err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), "UPDATE entries SET name='renamed',version=version+1 WHERE id=?", parent.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	})
	if !changed || result.Changed != markBatchSize+1 || result.Failed != 0 {
		t.Fatalf("ancestor snapshot survived a batch boundary: %+v", result)
	}
	assertDurableMarkProgress(t, l, result)
}

func TestProcessedMarksReadAvailabilityAfterBusyCallbacks(t *testing.T) {
	l, _, ids := markBatchFixture(t, 3)
	l.Busy = func(ctx context.Context, id int64) bool {
		if id == ids[0] {
			if err := l.db.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime_ns,kind,source,status,request_key,created)
 SELECT id,parent,name,'new.mp4',identity,size,mtime_ns,kind,'manual','prepared','mark-pending','2026-11-13T00:00:00.000Z' FROM entries WHERE id=?`, ids[1])
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		return id == ids[1]
	}
	result := l.SetProcessed(t.Context(), ids, true)
	if result.Changed != 2 || result.Failed != 1 || len(result.Failures) != 1 || result.Failures[0].ID != ids[1] {
		t.Fatalf("pending operation was not checked in the batch snapshot: %+v", result)
	}
	if got, want := result.Failures[0].Error.Code, domain.RecordOf(errPendingReview).Code; got != want {
		t.Fatalf("got failure %q, want availability failure %q before busy", got, want)
	}
	assertDurableMarkProgress(t, l, result)
}
