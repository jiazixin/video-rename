package library

import (
	"path/filepath"
	"testing"
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
	tx, err := l.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO marks(identity,size,mtime,source) VALUES(?,?,?,'marked')", e.Identity, e.Size, e.Mtime); err != nil {
		t.Fatal(err)
	}
	if p := read(); p.Processed != 0 {
		t.Fatal("uncommitted mark leaked", p)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if p := read(); p.Processed != 0 || l.progressCache[1] != original {
		t.Fatal("rollback invalidated visible state", p)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := l.DB.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO marks(identity,size,mtime,source) VALUES(?,?,?,'marked')", e.Identity, e.Size, e.Mtime)
	if p := read(); p.Processed != 1 {
		t.Fatal(p)
	}
	exec("INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[]',0,'running','','progress-cache')")
	exec("INSERT INTO run_items(run,entry,identity,size,mtime,status) VALUES(1,?,?,?,?,'running')", e.ID, e.Identity, e.Size, e.Mtime)
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
