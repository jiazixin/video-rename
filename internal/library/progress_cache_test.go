package library

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"video-rename/internal/storage"
)

func TestProgressCacheTracksCommittedFileAndTaskChanges(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "clip.mp4"))
	refresh(t, l, 1)
	e := entry(t, l, 1, "clip.mp4")
	read := func() DirectoryProgress {
		t.Helper()
		got, err := l.WithProgressBatch(t.Context(), []Entry{{ID: 1, Kind: "directory"}})
		if err != nil {
			t.Fatal(err)
		}
		return *got[0].Progress
	}
	if p := read(); p.Total != 1 || p.Processed != 0 || p.Active != 0 {
		t.Fatal(p)
	}
	original := l.progressCache[1]
	if p := read(); p.Total != 1 || l.progressCache[1] != original {
		t.Fatal("cache changed on stable read", p)
	}
	rollback := errors.New("test rollback")
	err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO marks(identity,size,mtime_ns,source) VALUES(?,?,?,'marked')", e.Identity, e.Size, e.ModifiedNS); err != nil {
			t.Fatal(err)
		}
		if p := read(); p.Processed != 0 {
			t.Fatal("uncommitted mark leaked", p)
		}

		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if p := read(); p.Processed != 0 || l.progressCache[1] != original {
		t.Fatal("rollback invalidated visible state", p)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), query, args...) }); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO marks(identity,size,mtime_ns,source) VALUES(?,?,?,'marked')", e.Identity, e.Size, e.ModifiedNS)
	if p := read(); p.Processed != 1 {
		t.Fatal(p)
	}
	exec("INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[]',0,'running','','progress-cache')")
	exec("INSERT INTO run_items(run,entry,identity,size,mtime_ns,status) VALUES(1,?,?,?,?,'running')", e.ID, e.Identity, e.Size, e.ModifiedNS)
	if p := read(); p.Active != 1 {
		t.Fatal(p)
	}
	exec("UPDATE runs SET status='completed' WHERE id=1")
	if p := read(); p.Active != 0 {
		t.Fatal(p)
	}
	exec("UPDATE entries SET size=size+1 WHERE id=?", e.ID)
	if p := read(); p.Processed != 0 {
		t.Fatal(p)
	}
	exec("UPDATE entries SET name='clip.jpg' WHERE id=?", e.ID)
	if p := read(); p.Total != 0 {
		t.Fatal(p)
	}
	exec("UPDATE entries SET name='clip.mp4' WHERE id=?", e.ID)
	if p := read(); p.Total != 1 {
		t.Fatal(p)
	}
	exec("UPDATE entries SET present=0 WHERE id=?", e.ID)
	if p := read(); p.Total != 0 {
		t.Fatal(p)
	}
}
