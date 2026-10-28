package library

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func fixture(t *testing.T) (*Library, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "videos")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dbpath := filepath.Join(base, "state", "library.sqlite")
	db, err := storage.Open(t.Context(), dbpath)
	if err != nil {
		t.Fatal(err)
	}
	l, err := New(t.Context(), db, root, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return l, root, dbpath
}
func refresh(t *testing.T, l *Library, id int64) {
	t.Helper()
	if err := l.Refresh(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}
func entry(t *testing.T, l *Library, parent int64, name string) Entry {
	t.Helper()
	var id int64
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM entries WHERE parent=? AND name=? AND present=1", parent, name).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	e, err := l.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func put(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("video fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
}
func TestRenameRestoreParentAndRestart(t *testing.T) {
	l, root, dbpath := fixture(t)
	if err := os.Mkdir(filepath.Join(root, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "folder", "a.mp4"))
	refresh(t, l, 1)
	dir := entry(t, l, 1, "folder")
	refresh(t, l, dir.ID)
	file := entry(t, l, dir.ID, "a.mp4")
	first, err := l.Rename(t.Context(), RenameRequest{ID: file.ID, Version: file.Version, Name: "b.mp4", Key: "manual-a"})
	if err != nil {
		t.Fatal(err)
	}
	file, _ = l.Get(t.Context(), file.ID)
	second, err := l.Rename(t.Context(), RenameRequest{ID: file.ID, Version: file.Version, Name: "c.mp4", Key: "manual-b"})
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = l.Get(t.Context(), dir.ID)
	if _, err = l.Rename(t.Context(), RenameRequest{ID: dir.ID, Version: dir.Version, Name: "new folder", Key: "manual-dir"}); err != nil {
		t.Fatal(err)
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
	if _, err = l.Restore(t.Context(), second.ID, "restore-b"); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Restore(t.Context(), first.ID, "restore-a"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "new folder", "a.mp4")); err != nil {
		t.Fatal(err)
	}
	file, _ = l.Get(t.Context(), file.ID)
	if file.Processed != "" || file.RestoreID != 0 {
		t.Fatalf("bad restore chain %+v", file)
	}
	snap, _ := domain.StatSnapshot(filepath.Join(root, "new folder", "a.mp4"))
	if _, err = l.AutoCommit(t.Context(), AutoCommitOptions{ID: file.ID, Snap: snap, Title: "again", Key: "auto-test", Run: 1}); err != nil {
		t.Fatal("restored file must allow automatic rename", err)
	}
}
func TestConflictTraversalReplacementAndIdempotency(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	put(t, filepath.Join(root, "occupied.mp4"))
	refresh(t, l, 1)
	e := entry(t, l, 1, "a.mp4")
	for _, name := range []string{"../escape.mp4", "bad/name.mp4", "occupied.mp4", "bad."} {
		if _, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: name, Key: "bad:" + name}); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	o, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: "b.mp4", Key: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	e, _ = l.Get(t.Context(), e.ID)
	o2, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: "b.mp4", Key: "same-key"})
	if err != nil || o2.ID != o.ID {
		t.Fatalf("idempotency %v %+v", err, o2)
	}
	put(t, filepath.Join(root, "a.mp4"))
	if _, err = l.Restore(t.Context(), o.ID, "restore-conflict"); err == nil {
		t.Fatal("restore must not overwrite")
	}
	if err = os.Remove(filepath.Join(root, "b.mp4")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "b.mp4"))
	refresh(t, l, 1)
	replacement := entry(t, l, 1, "b.mp4")
	if replacement.Processed != "" {
		t.Fatal("replacement inherited protection")
	}
}
func TestSymlinkBlocked(t *testing.T) {
	l, root, _ := fixture(t)
	outside := t.TempDir()
	put(t, filepath.Join(outside, "secret.mp4"))
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	e := entry(t, l, 1, "link")
	if _, err := l.Path(t.Context(), e.ID); err == nil {
		t.Fatal("symlink followed")
	}
}
func TestPreparedRecovery(t *testing.T) {
	l, root, dbpath := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	e := entry(t, l, 1, "a.mp4")
	res, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime_ns,kind,source,status,request_key,created) VALUES(?,1,'a.mp4','b.mp4',?,?,?,'file','automatic','prepared','crash',?)", e.ID, e.Identity, e.Size, e.ModifiedNS, storage.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if er := os.Rename(filepath.Join(root, "a.mp4"), filepath.Join(root, "b.mp4")); er != nil {
		t.Fatal(er)
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
	o, _ := l.Operation(t.Context(), id)
	if o.Status != "committed" {
		t.Fatal(o.Status)
	}
	e, _ = l.Get(t.Context(), e.ID)
	if e.Name != "b.mp4" || e.Processed != "automatic" {
		t.Fatalf("bad recovery %+v", e)
	}
}
func TestKindFilterAudioAndOther(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"a.mp4", "b.jpg", "c.mp3", "d.flac", "e.txt"} {
		put(t, filepath.Join(root, name))
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	names := func(kind string) string {
		p, err := l.ListOrdered(t.Context(), ListOrderedOptions{ID: 1, Offset: 0, Limit: 100, Generation: 0, Query: "", Kind: kind, Status: "", Force: false, SortBy: "name_asc"})
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		for _, e := range p.Items {
			if got != "" {
				got += ","
			}
			got += e.Name
		}
		return got
	}
	if got := names("audio"); got != "sub,c.mp3,d.flac" {
		t.Fatalf("audio filter: %s", got)
	}
	if got := names("other"); got != "e.txt" {
		t.Fatalf("other should exclude media and audio: %s", got)
	}
	if got := names("video"); got != "sub,a.mp4" {
		t.Fatalf("video filter: %s", got)
	}
}

func TestPagedDirectoryDoesNotRecurse(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 260 {
		put(t, filepath.Join(root, fmt.Sprintf("%03d.mp4", i)))
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "nested", "hidden.mp4"))
	refresh(t, l, 1)
	p, err := l.ListOrdered(t.Context(), ListOrderedOptions{ID: 1, Offset: 0, Limit: 100, Generation: 0, Query: "", Kind: "", Status: "", Force: false, SortBy: "name_asc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 100 || p.Next == 0 || p.Items[0].Kind != "directory" {
		t.Fatalf("pagination %+v", p)
	}
	var count int
	if err = l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM entries").Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 262 {
		t.Fatalf("unexpected recursive index %d", count)
	}
	next, err := l.ListOrdered(t.Context(), ListOrderedOptions{ID: 1, Offset: p.Next, Limit: 100, Generation: p.Generation, Query: "", Kind: "", Status: "", Force: false, SortBy: "name_asc"})
	if err != nil || len(next.Items) != 100 {
		t.Fatalf("next %v", err)
	}
}

func TestDatabaseFailureStopsUnrecordedRename(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	e := entry(t, l, 1, "a.mp4")
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "PRAGMA query_only=ON") }); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: "b.mp4", Key: "read-only-db"}); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "a.mp4")); err != nil {
		t.Fatal("file moved before prepared persisted")
	}
	storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "PRAGMA query_only=OFF") })
}
func TestPostRenameDatabaseFailureRecovers(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	e := entry(t, l, 1, "a.mp4")
	_, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "CREATE TRIGGER reject_commit BEFORE UPDATE OF status ON operations WHEN NEW.status='committed' BEGIN SELECT RAISE(ABORT,'injected final commit failure'); END;")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: "b.mp4", Key: "failed-final-record"}); err == nil {
		t.Fatal("missing injected error")
	}
	if !l.Halted() {
		t.Fatal("new commits not halted")
	}
	if _, err = os.Stat(filepath.Join(root, "b.mp4")); err != nil {
		t.Fatal("expected actual rename")
	}
	if _, err = storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DROP TRIGGER reject_commit") }); err != nil {
		t.Fatal(err)
	}
	if err = l.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	e, err = l.Get(t.Context(), e.ID)
	if err != nil || e.Processed != "manual" || e.Name != "b.mp4" {
		t.Fatalf("recovery %+v %v", e, err)
	}
}
func TestAmbiguousRecoveryBlocksRelatedDirectory(t *testing.T) {
	l, root, _ := fixture(t)
	os.Mkdir(filepath.Join(root, "dir"), 0o700)
	put(t, filepath.Join(root, "dir", "a.mp4"))
	put(t, filepath.Join(root, "dir", "b.mp4"))
	refresh(t, l, 1)
	d := entry(t, l, 1, "dir")
	refresh(t, l, d.ID)
	e := entry(t, l, d.ID, "a.mp4")
	_, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime_ns,kind,source,status,request_key,created) VALUES(?,?,'a.mp4','b.mp4',?,?,?,'file','automatic','prepared','ambiguous',?)", e.ID, d.ID, e.Identity, e.Size, e.ModifiedNS, storage.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = l.CheckAvailable(t.Context(), e.ID); err == nil {
		t.Fatal("ambiguous file allowed")
	}
	if _, err = l.Rename(t.Context(), RenameRequest{ID: d.ID, Version: d.Version, Name: "other", Key: "blocked-parent"}); err == nil {
		t.Fatal("related directory allowed")
	}
}

func TestRestoreUnchangedAutomaticNameClearsProtection(t *testing.T) {
	l, root, _ := fixture(t)
	path := filepath.Join(root, "same.mp4")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM entries WHERE name='same.mp4'").Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := domain.StatSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	op, err := l.AutoCommit(t.Context(), AutoCommitOptions{ID: id, Snap: snap, Title: "same", Key: "unchanged-auto", Run: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Restore(t.Context(), op.ID, "undo-unchanged-auto"); err != nil {
		t.Fatal(err)
	}
	entry, err := l.Get(t.Context(), id)
	if err != nil || entry.Processed != "" {
		t.Fatal(entry, err)
	}
	if _, err = l.AutoCommit(t.Context(), AutoCommitOptions{ID: id, Snap: snap, Title: "different", Key: "retry-auto", Run: 0}); err != nil {
		t.Fatal(err)
	}
}

func TestLowercaseExtensions(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, title := range []string{"Original", "NewTitle"} {
			t.Run(fmt.Sprintf("automatic=%t/title=%s", automatic, title), func(t *testing.T) {
				l, root, _ := fixture(t)
				put(t, filepath.Join(root, "Original.MP4"))
				refresh(t, l, 1)
				e := entry(t, l, 1, "Original.MP4")
				var op Operation
				var err error
				if automatic {
					snap, er := domain.StatSnapshot(filepath.Join(root, e.Name))
					if er != nil {
						t.Fatal(er)
					}
					op, err = l.AutoCommit(t.Context(), AutoCommitOptions{ID: e.ID, Snap: snap, Title: title, Key: "lower-auto", Run: 0})
				} else {
					op, err = l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: title + ".Mp4", Key: "lower-manual"})
				}
				if err != nil {
					t.Fatal(err)
				}
				if op.New != title+".mp4" {
					t.Fatalf("unexpected target: %s", op.New)
				}
				files, err := os.ReadDir(root)
				if err != nil || len(files) != 1 || files[0].Name() != op.New {
					t.Fatalf("target not renamed: %v, %v", files, err)
				}
				if !automatic {
					retry, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: title + ".Mp4", Key: "lower-manual"})
					if err != nil || retry.ID != op.ID {
						t.Fatalf("retry: %+v, %v", retry, err)
					}
				}
				if _, err = l.Restore(t.Context(), op.ID, "restore-case"); err != nil {
					t.Fatal(err)
				}
				files, err = os.ReadDir(root)
				if err != nil || len(files) != 1 || files[0].Name() != "Original.MP4" {
					t.Fatalf("original not restored: %v, %v", files, err)
				}
			})
		}
	}
}

func TestAutomaticSuccessResolvesOnlyMatchingFailures(t *testing.T) {
	l, root, _ := fixture(t)
	path := filepath.Join(root, "retry.mp4")
	put(t, path)
	refresh(t, l, 1)
	e := entry(t, l, 1, "retry.mp4")
	for _, record := range []struct {
		action string
		entry  int64
	}{{"automatic", e.ID}, {"automatic", e.ID}, {"scan", e.ID}, {"automatic", 999}} {
		if err := l.Event(t.Context(), EventInput{Category: "failure", Action: record.action, Entry: record.entry, Operation: 0, Run: 0, Old: "retry.mp4", New: "", Message: "failed"}); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := domain.StatSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := snap
	bad.Size++
	if _, err = l.AutoCommit(t.Context(), AutoCommitOptions{ID: e.ID, Snap: bad, Title: "new", Key: "retry-failed", Run: 0}); err == nil {
		t.Fatal("changed source accepted")
	}
	var count int
	if err = l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events WHERE category='failure'").Scan(&count)
	}); err != nil || count != 4 {
		t.Fatal(count, err)
	}
	if _, err = l.AutoCommit(t.Context(), AutoCommitOptions{ID: e.ID, Snap: snap, Title: "new", Key: "retry-success", Run: 0}); err != nil {
		t.Fatal(err)
	}
	if err = l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events WHERE category='failure'").Scan(&count)
	}); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if err = l.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM events WHERE category='success' AND entry=?", e.ID).Scan(&count)
	}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestManualExtensionChangeAndRestore(t *testing.T) {
	for _, tc := range []struct{ old, requested, want string }{
		{"clip.mp4", "clip.MKV", "clip.mkv"},
		{"photo.JPEG", "photo.JPEG", "photo.jpg"},
		{"photo.png", "photo.JpEg", "photo.jpg"},
		{"photo.jpeg", "renamed.jpeg", "renamed.jpg"},
	} {
		t.Run(tc.old+"/"+tc.requested, func(t *testing.T) {
			l, root, _ := fixture(t)
			put(t, filepath.Join(root, tc.old))
			refresh(t, l, 1)
			e := entry(t, l, 1, tc.old)
			// Changing only the extension must still reject an occupied target.
			put(t, filepath.Join(root, tc.want))
			if _, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: tc.requested, Key: "conflict"}); err == nil {
				t.Fatal("extension change overwrote an occupied target")
			}
			if err := os.Remove(filepath.Join(root, tc.want)); err != nil {
				t.Fatal(err)
			}
			op, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: tc.requested, Key: "extension"})
			if err != nil || op.New != tc.want {
				t.Fatalf("op=%+v err=%v", op, err)
			}
			data, err := os.ReadFile(filepath.Join(root, tc.want))
			if err != nil || string(data) != "video fixture" {
				t.Fatalf("data=%q err=%v", data, err)
			}
			retry, err := l.Rename(t.Context(), RenameRequest{ID: e.ID, Version: e.Version, Name: tc.requested, Key: "extension"})
			if err != nil || retry.ID != op.ID {
				t.Fatalf("retry=%+v err=%v", retry, err)
			}
			if _, err := l.Restore(t.Context(), op.ID, "restore-extension"); err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(root)
			if err != nil || len(files) != 1 || files[0].Name() != tc.old {
				t.Fatalf("files=%v err=%v", files, err)
			}
		})
	}
}
