package library

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestProcessedMarksCancellationBetweenBatches(t *testing.T) {
	l, _, _ := markBatchFixture(t, markBatchSize+1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := l.SetProcessedWithProgress(ctx, []int64{1}, true, func(result MarkResult) {
		assertDurableMarkProgress(t, l, result)
		if result.Changed == markBatchSize {
			cancel()
		}
	})
	if !r.Interrupted || r.Changed != markBatchSize || r.Unchanged != 0 {
		t.Fatalf("cancelled traversal reported as complete: %+v", r)
	}
	assertDurableMarkProgress(t, l, r)
	// A fresh request finishes the remaining file and preserves the committed batch.
	r = l.SetProcessed(t.Context(), []int64{1}, true)
	if r.Interrupted || r.Changed != 1 || r.Unchanged != markBatchSize || r.Failed != 0 {
		t.Fatalf("retry did not finish remaining work: %+v", r)
	}
}

func TestProcessedMarksCancellationBetweenTargets(t *testing.T) {
	l, _, ids := markBatchFixture(t, markBatchSize+1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := l.SetProcessedWithProgress(ctx, ids, true, func(result MarkResult) {
		assertDurableMarkProgress(t, l, result)
		if result.Changed == markBatchSize {
			cancel()
		}
	})
	if !r.Interrupted || r.Changed != markBatchSize || r.Unchanged != 0 {
		t.Fatalf("cancelled targets reported as complete: %+v", r)
	}
	assertDurableMarkProgress(t, l, r)
}

func markBatchFixture(t *testing.T, count int) (*Library, string, []int64) {
	t.Helper()
	l, root, _ := fixture(t)
	for i := range count {
		put(t, filepath.Join(root, fmt.Sprintf("%04d.mp4", i)))
	}
	refresh(t, l, 1)
	var ids []int64
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(t.Context(), "SELECT id FROM entries WHERE parent=1 AND present=1 ORDER BY id")
		if err != nil {
			return err
		}
		ids, err = storage.CollectRows(rows, func(rows *sql.Rows) (int64, error) {
			var id int64
			err := rows.Scan(&id)
			return id, err
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(ids) != count {
		t.Fatalf("indexed %d of %d files", len(ids), count)
	}
	return l, root, ids
}

// The read pool sees only committed WAL state, even while a write transaction exists.
func assertDurableMarkProgress(t *testing.T, l *Library, result MarkResult) {
	t.Helper()
	var count int64
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM marks").Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != result.Changed {
		t.Fatalf("progress differs from durable marks: count=%d result=%+v", count, result)
	}
}

func TestProcessedMarksBatchDatabaseFailureRollsBack(t *testing.T) {
	for _, failure := range []string{"insert", "commit"} {
		t.Run(failure, func(t *testing.T) {
			l, _, ids := markBatchFixture(t, markBatchSize+2)
			target := ids[markBatchSize/2]
			trigger := fmt.Sprintf(`CREATE TRIGGER reject_mark BEFORE INSERT ON marks
WHEN NEW.identity=(SELECT identity FROM entries WHERE id=%d)
BEGIN SELECT RAISE(ABORT,'injected mark insert failure'); END`, target)
			if failure == "commit" {
				// A deferred foreign key lets every INSERT succeed, then rejects COMMIT.
				trigger = fmt.Sprintf(`CREATE TRIGGER reject_mark AFTER INSERT ON marks
WHEN NEW.identity=(SELECT identity FROM entries WHERE id=%d)
BEGIN UPDATE marks SET operation=-1 WHERE identity=NEW.identity AND size=NEW.size AND mtime_ns=NEW.mtime_ns; END`, target)
			}
			if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), trigger) }); err != nil {
				t.Fatal(err)
			}
			if failure == "commit" {
				if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
					return tx.ExecContext(t.Context(), "PRAGMA defer_foreign_keys=ON")
				}); err != nil {
					t.Fatal(err)
				}
			}
			r := l.SetProcessedWithProgress(t.Context(), ids, true, func(result MarkResult) {
				assertDurableMarkProgress(t, l, result)
			})
			if r.Interrupted || r.Changed != 2 || r.Unchanged != 0 || r.Failed != markBatchSize || len(r.Failures) != 100 {
				t.Fatalf("failed batch reported partial success: %+v", r)
			}
			assertDurableMarkProgress(t, l, r)
			var firstBatchMarks int
			if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), `SELECT count(*) FROM marks m
JOIN entries e ON e.identity=m.identity AND e.size=m.size AND e.mtime_ns=m.mtime_ns WHERE e.id<=?`, ids[markBatchSize-1]).Scan(&firstBatchMarks)
			}); err != nil {
				t.Fatal(err)
			}
			if firstBatchMarks != 0 {
				t.Fatalf("failed transaction retained %d marks", firstBatchMarks)
			}
			if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DROP TRIGGER reject_mark") }); err != nil {
				t.Fatal(err)
			}
			r = l.SetProcessed(t.Context(), ids, true)
			if r.Interrupted || r.Changed != markBatchSize || r.Unchanged != 2 || r.Failed != 0 {
				t.Fatalf("retry did not preserve later committed batch: %+v", r)
			}
		})
	}
}

func TestProcessedMarksBatchValidationFailureKeepsValidPeers(t *testing.T) {
	l, root, ids := markBatchFixture(t, 4)
	changed, err := l.IndexedEntry(t.Context(), ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, changed.Name), []byte("changed file has a different size"), 0o600); err != nil {
		t.Fatal(err)
	}
	l.Busy = func(_ context.Context, id int64) bool { return id == ids[0] }
	r := l.SetProcessedWithProgress(t.Context(), ids, true, func(result MarkResult) {
		assertDurableMarkProgress(t, l, result)
	})
	if r.Interrupted || r.Changed != 2 || r.Unchanged != 0 || r.Failed != 2 {
		t.Fatalf("invalid files prevented valid peers from committing: %+v", r)
	}
	assertDurableMarkProgress(t, l, r)
	for _, id := range ids[:2] {
		e, err := l.Get(t.Context(), id)
		if err != nil || e.Processed != "" {
			t.Fatalf("invalid file received a mark: %+v %v", e, err)
		}
	}
}

func TestProcessedMarksDuplicateMissingTargetsFailOnce(t *testing.T) {
	l, _, ids := markBatchFixture(t, 1)
	missing := ids[0] + 1
	r := l.SetProcessedWithProgress(t.Context(), []int64{missing, ids[0], missing, ids[0]}, true, func(result MarkResult) {
		assertDurableMarkProgress(t, l, result)
	})
	if r.Interrupted || r.Changed != 1 || r.Unchanged != 0 || r.Failed != 1 || len(r.Failures) != 1 || r.Failures[0].ID != missing {
		t.Fatalf("duplicate targets were counted more than once: %+v", r)
	}
	assertDurableMarkProgress(t, l, r)
}

func TestProcessedMarksRevalidateMediaTypeBeforeCommit(t *testing.T) {
	l, root, ids := markBatchFixture(t, 3)
	changed, err := l.IndexedEntry(t.Context(), ids[1])
	if err != nil {
		t.Fatal(err)
	}
	const unsupported = "renamed.txt"
	l.Busy = func(_ context.Context, id int64) bool {
		if id == ids[0] {
			// Every target is collected before validation starts. Simulate another
			// operation changing a later target while it waits in the batch.
			if err := os.Rename(filepath.Join(root, changed.Name), filepath.Join(root, unsupported)); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(t.Context(), "UPDATE entries SET name=?,version=version+1 WHERE id=?", unsupported, changed.ID)
			}); err != nil {
				t.Fatal(err)
			}
		}
		return false
	}
	r := l.SetProcessedWithProgress(t.Context(), ids, true, func(result MarkResult) {
		assertDurableMarkProgress(t, l, result)
	})
	if r.Interrupted || r.Changed != 2 || r.Unchanged != 0 || r.Failed != 1 || len(r.Failures) != 1 || r.Failures[0].ID != changed.ID {
		t.Fatalf("changed media type received a mark: %+v", r)
	}
	e, err := l.Get(t.Context(), changed.ID)
	if err != nil || e.Name != unsupported || e.Processed != "" {
		t.Fatalf("unsupported renamed file received a mark: %+v %v", e, err)
	}
	assertDurableMarkProgress(t, l, r)
}

func TestProcessedMarksCancellationDuringBatchValidation(t *testing.T) {
	l, _, ids := markBatchFixture(t, 8)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	l.Busy = func(_ context.Context, id int64) bool {
		if id == ids[4] {
			cancel()
		}
		return false
	}
	r := l.SetProcessedWithProgress(ctx, ids, true, func(result MarkResult) {
		assertDurableMarkProgress(t, l, result)
	})
	if !r.Interrupted || r.Changed != 0 || r.Unchanged != 0 {
		t.Fatalf("cancelled uncommitted batch reported success: %+v", r)
	}
	assertDurableMarkProgress(t, l, r)
	l.Busy = nil
	r = l.SetProcessed(t.Context(), ids, true)
	if r.Interrupted || r.Changed != 8 || r.Unchanged != 0 || r.Failed != 0 {
		t.Fatalf("cancelled batch left partial marks: %+v", r)
	}
}

func TestProcessedMarksProgressReleasesWriterConnection(t *testing.T) {
	l, _, ids := markBatchFixture(t, 2)
	r := l.SetProcessedWithProgress(t.Context(), ids, true, func(result MarkResult) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if _, err := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "UPDATE library SET root=root WHERE id=1")
		}); err != nil {
			t.Fatalf("progress callback cannot acquire writer connection: %v", err)
		}
		assertDurableMarkProgress(t, l, result)
	})
	if r.Interrupted || r.Changed != 2 || r.Failed != 0 {
		t.Fatalf("marking failed after progress callback write: %+v", r)
	}
}

func TestProcessedMarksBatchUnmarkFailureRollsBack(t *testing.T) {
	l, _, ids := markBatchFixture(t, 3)
	if r := l.SetProcessed(t.Context(), ids, true); r.Changed != 3 || r.Failed != 0 {
		t.Fatal(r)
	}
	trigger := fmt.Sprintf(`CREATE TRIGGER reject_unmark BEFORE DELETE ON marks
WHEN OLD.identity=(SELECT identity FROM entries WHERE id=%d)
BEGIN SELECT RAISE(ABORT,'injected unmark failure'); END`, ids[1])
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), trigger) }); err != nil {
		t.Fatal(err)
	}
	r := l.SetProcessedWithProgress(t.Context(), ids, false, func(result MarkResult) {
		var count int64
		if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
			return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM marks").Scan(&count)
		}); err != nil {
			t.Fatal(err)
		}
		if count != 3-result.Changed {
			t.Fatalf("unmark progress differs from durable marks: count=%d result=%+v", count, result)
		}
	})
	if r.Interrupted || r.Changed != 0 || r.Unchanged != 0 || r.Failed != 3 {
		t.Fatalf("unmark failure reported partial success: %+v", r)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DROP TRIGGER reject_unmark") }); err != nil {
		t.Fatal(err)
	}
	if r = l.SetProcessed(t.Context(), ids, false); r.Changed != 3 || r.Unchanged != 0 || r.Failed != 0 {
		t.Fatalf("unmark rollback lost protected files: %+v", r)
	}
	if r = l.SetProcessed(t.Context(), ids, false); r.Changed != 0 || r.Unchanged != 3 || r.Failed != 0 {
		t.Fatalf("repeated unmark changed cleared files: %+v", r)
	}
}

func TestProcessedMarksRecursiveAndPersistent(t *testing.T) {
	l, root, dbpath := fixture(t)
	if err := os.MkdirAll(filepath.Join(root, "dir", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "dir", "a.mp4"))
	put(t, filepath.Join(root, "dir", "nested", "b.mp4"))
	put(t, filepath.Join(root, "dir", "image.jpg"))
	put(t, filepath.Join(root, "dir", "nested", "song.mp3"))
	put(t, filepath.Join(root, "dir", "notes.txt"))
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "dir", "link")); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	d := entry(t, entryOptions{l: l, parent: 1, name: "dir"})
	r := l.SetProcessed(t.Context(), []int64{d.ID, d.ID}, true)
	if r.Changed != 4 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
	a := entry(t, entryOptions{l: l, parent: d.ID, name: "a.mp4"})
	if a.Processed != "marked" || a.RestoreID != 0 {
		t.Fatalf("%+v", a)
	}
	history, err := l.History(t.Context(), a.ID, "")
	if err != nil || len(history.Items) != 0 {
		t.Fatal(history, err)
	}
	put(t, filepath.Join(root, "dir", "new.mp4"))
	refresh(t, l, d.ID)
	if e := entry(t, entryOptions{l: l, parent: d.ID, name: "new.mp4"}); e.Processed != "" {
		t.Fatal("new file inherited mark")
	}
	l.db.Close()
	db, err := storage.Open(t.Context(), dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err = New(t.Context(), db, root, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a, _ = l.Get(t.Context(), a.ID)
	if a.Processed != "marked" {
		t.Fatal("mark lost on restart")
	}
	image := entry(t, entryOptions{l: l, parent: d.ID, name: "image.jpg"})
	if image.Processed != "marked" || image.RestoreID != 0 {
		t.Fatalf("image protection lost on restart: %+v", image)
	}
	if notes := entry(t, entryOptions{l: l, parent: d.ID, name: "notes.txt"}); notes.Processed != "" {
		t.Fatal("marked unsupported file")
	}
	r = l.SetProcessed(t.Context(), []int64{d.ID}, false)
	if r.Changed != 4 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestMediaMarksProtectManualNamesAndNumberedFiles(t *testing.T) {
	for _, ext := range []string{"jpg", "mp3"} {
		t.Run(ext, func(t *testing.T) {
			l, root, _ := fixture(t)
			name := "original." + ext
			put(t, filepath.Join(root, name))
			refresh(t, l, 1)
			e := entry(t, entryOptions{l: l, parent: 1, name: name})
			if r := l.SetProcessed(t.Context(), []int64{e.ID}, true); r.Changed != 1 || r.Failed != 0 {
				t.Fatal(r)
			}
			op, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: "my-name." + ext, Key: "manual-protected-name"})
			if err != nil {
				t.Fatal(err)
			}
			e, err = l.Get(t.Context(), e.ID)
			if err != nil || e.Processed != "marked" || e.RestoreID != op.ID {
				t.Fatalf("manual rename lost protection/history: %+v %v", e, err)
			}
			if r := l.SetProcessed(t.Context(), []int64{e.ID}, false); r.Changed != 1 || r.Failed != 0 {
				t.Fatal(r)
			}
			settings := domain.DefaultNumberingSettings()
			batches, err := l.PreviewNumberingTree(t.Context(), 1, settings)
			if err != nil || len(batches) != 1 {
				t.Fatalf("unmarked file cannot be numbered: %+v %v", batches, err)
			}
			if _, err = l.ApplyNumbering(t.Context(), batches[0].ID, nil); err != nil {
				t.Fatal(err)
			}
			batches, err = l.PreviewNumberingTree(t.Context(), 1, settings)
			if err != nil || len(batches) != 1 || batches[0].Total != 1 {
				t.Fatalf("numbering history incorrectly protects file: %+v %v", batches, err)
			}
			if r := l.SetProcessed(t.Context(), []int64{e.ID}, true); r.Changed != 1 || r.Failed != 0 {
				t.Fatalf("cannot protect numbered file: %+v", r)
			}
			batches, err = l.PreviewNumberingTree(t.Context(), 1, settings)
			if err != nil || len(batches) != 0 {
				t.Fatalf("all-protected directory should be skipped: %+v %v", batches, err)
			}
		})
	}
}

func TestProcessedMarksPreserveHistoryAndValidateIdentity(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	a := entry(t, entryOptions{l: l, parent: 1, name: "a.mp4"})
	op, err := l.Rename(t.Context(), RenameRequest{ID: a.ID, Version: a.Version, Name: "b.mp4", Key: "mark-history"})
	if err != nil {
		t.Fatal(err)
	}
	r := l.SetProcessed(t.Context(), []int64{a.ID}, true)
	if r.Changed != 0 {
		t.Fatal("overwrote existing processed origin")
	}
	r = l.SetProcessed(t.Context(), []int64{a.ID}, false)
	if r.Changed != 1 {
		t.Fatalf("%+v", r)
	}
	a, _ = l.Get(t.Context(), a.ID)
	if a.Processed != "" || a.RestoreID != op.ID || a.Name != "b.mp4" {
		t.Fatalf("%+v", a)
	}
	if _, err = l.Restore(t.Context(), op.ID, "restore-after-unmark"); err != nil {
		t.Fatal(err)
	}
	l.Busy = func(context.Context, int64) bool { return true }
	if r = l.SetProcessed(t.Context(), []int64{a.ID}, true); r.Failed != 1 {
		t.Fatal("busy file accepted")
	}
	l.Busy = nil
	if err = os.WriteFile(filepath.Join(root, "a.mp4"), []byte("different content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r = l.SetProcessed(t.Context(), []int64{a.ID}, true); r.Failed != 1 {
		t.Fatal("stale file accepted")
	}
}

func TestRenameKeepsDirectoryPageAvailable(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	put(t, filepath.Join(root, "b.mp4"))
	refresh(t, l, 1)
	before, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 100, Query: "", Kind: "", Status: "", SortBy: "name_asc"})
	if err != nil {
		t.Fatal(err)
	}
	e := entry(t, entryOptions{l: l, parent: 1, name: "a.mp4"})
	if _, err = l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: "z.mp4", Key: "keep-index"}); err != nil {
		t.Fatal(err)
	}
	after, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 100, Query: "", Kind: "", Status: "", SortBy: "name_asc"})
	if err != nil || len(after.Items) != 2 || after.Generation != before.Generation || after.Items[1].Name != "z.mp4" {
		t.Fatalf("%+v %v", after, err)
	}
}

func TestProcessedMarksHardLinksAndChangedContent(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	if err := os.Link(filepath.Join(root, "a.mp4"), filepath.Join(root, "alias.mp4")); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	a := entry(t, entryOptions{l: l, parent: 1, name: "a.mp4"})
	alias := entry(t, entryOptions{l: l, parent: 1, name: "alias.mp4"})
	r := l.SetProcessed(t.Context(), []int64{a.ID, alias.ID, a.ID}, true)
	if r.Changed != 1 || r.Unchanged != 1 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
	alias, _ = l.Get(t.Context(), alias.ID)
	if alias.Processed != "marked" {
		t.Fatal("hard link did not share mark")
	}
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("new content of another size"), 0o600); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	a, _ = l.Get(t.Context(), a.ID)
	if a.Processed != "" {
		t.Fatal("changed content inherited mark")
	}
}

func TestAutomaticSuccessDiagnosticsPersistWithHistory(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	e := entry(t, entryOptions{l: l, parent: 1, name: "a.mp4"})
	snap, err := domain.StatSnapshot(filepath.Join(root, "a.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	op, err := l.AutoCommit(t.Context(), AutoCommitOptions{ID: e.ID, Snap: snap, Title: "new-name", Key: "success-diagnostics", Run: 0, Diagnostics: []domain.ModelDiagnostics{domain.ModelDiagnostics{Request: `{"messages":[]}`, Response: `{"choices":[],"usage":10}`}}})
	if err != nil {
		t.Fatal(err)
	}
	var request, response string
	if err = l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT raw_request,raw_response FROM events WHERE operation=?", op.ID).Scan(&request, &response)
	}); err != nil || request == "" || response == "" {
		t.Fatal(request, response, err)
	}
}
