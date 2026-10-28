package library

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/storage"
)

func TestDirectoryProgressRequiresCompleteIndex(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "a.mp4"))
	put(t, filepath.Join(root, "nested", "b.mp4"))
	refresh(t, l, 1)
	get := func() *DirectoryProgress {
		e, err := l.Get(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := l.WithProgressBatch(t.Context(), []Entry{e})
		if err != nil {
			t.Fatal(err)
		}
		return entries[0].Progress
	}
	if p := get(); p.Unknown != 1 || p.Total != 1 {
		t.Fatalf("%+v", p)
	}
	a := entry(t, entryOptions{l: l, parent: 1, name: "a.mp4"})
	l.SetProcessed(t.Context(), []int64{a.ID}, true)
	if p := get(); p.Unknown != 1 || p.Processed != 1 {
		t.Fatalf("%+v", p)
	}
	d := entry(t, entryOptions{l: l, parent: 1, name: "nested"})
	refresh(t, l, d.ID)
	if p := get(); p.Unknown != 0 || p.Total != 2 || p.Processed != 1 {
		t.Fatalf("%+v", p)
	}
	l.SetProcessed(t.Context(), []int64{d.ID}, true)
	if p := get(); p.Processed != 2 || p.Total != 2 {
		t.Fatalf("%+v", p)
	}
	l.SetProcessed(t.Context(), []int64{1}, false)
	if p := get(); p.Processed != 0 || p.Total != 2 {
		t.Fatalf("%+v", p)
	}
}

func TestDirectoryContentsUseLastCompleteDirectIndex(t *testing.T) {
	l, root, _ := fixture(t)
	directory := filepath.Join(root, "album")
	if err := os.MkdirAll(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clip.MP4", "photo.JPG", "song.MP3", "recording.flac", "notes.txt"} {
		put(t, filepath.Join(directory, name))
	}
	put(t, filepath.Join(directory, "nested", "deeper.mp4"))
	put(t, filepath.Join(directory, "nested", "deeper.mp3"))
	refresh(t, l, 1)
	album := entry(t, entryOptions{l: l, parent: 1, name: "album"})
	get := func() *DirectoryContents {
		t.Helper()
		entries, err := l.WithProgressBatch(t.Context(), []Entry{album})
		if err != nil {
			t.Fatal(err)
		}
		return entries[0].Contents
	}
	if c := get(); c != nil {
		t.Fatalf("unindexed directory has counts: %+v", c)
	}
	refresh(t, l, album.ID)
	if c := get(); c == nil || c.Directories != 1 || c.Videos != 1 || c.Images != 1 || c.Audio != 2 {
		t.Fatalf("direct counts: %+v", c)
	}
	put(t, filepath.Join(directory, "new.webp"))
	put(t, filepath.Join(directory, "new.wav"))
	if c := get(); c == nil || c.Images != 1 || c.Audio != 2 {
		t.Fatalf("counts changed before refresh: %+v", c)
	}
	refresh(t, l, album.ID)
	if c := get(); c == nil || c.Images != 2 || c.Audio != 3 {
		t.Fatalf("refreshed counts: %+v", c)
	}
}

func TestProgressBatchOverlappingScopesAndFreshState(t *testing.T) {
	l, root, _ := fixture(t)
	for _, dir := range []string{"a/nested", "b", "empty"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a/one.MP4", "a/nested/two.mp4", "b/three.mp4", "b/photo.jpg", "b/song.mp3", "b/note.txt"} {
		put(t, filepath.Join(root, name))
	}
	refresh(t, l, 1)
	a, b, empty := entry(t, entryOptions{l: l, parent: 1, name: "a"}), entry(t, entryOptions{l: l, parent: 1, name: "b"}), entry(t, entryOptions{l: l, parent: 1, name: "empty"})
	refresh(t, l, a.ID)
	refresh(t, l, b.ID)
	refresh(t, l, empty.ID)
	nested := entry(t, entryOptions{l: l, parent: a.ID, name: "nested"})
	refresh(t, l, nested.ID)
	file := entry(t, entryOptions{l: l, parent: a.ID, name: "one.MP4"})
	rootEntry, err := l.Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	input := []Entry{rootEntry, a, nested, b, empty, a, file}
	check := func(processed, active, unknown int) []Entry {
		t.Helper()
		got, err := l.WithProgressBatch(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if p := got[0].Progress; p.Total != 3 || p.Processed != processed || p.Active != active || p.Unknown != unknown {
			t.Fatalf("root: %+v", p)
		}
		if p := got[1].Progress; p.Total != 2 {
			t.Fatalf("a: %+v", p)
		}
		if *got[1].Progress != *got[5].Progress {
			t.Fatal("duplicate scopes differ")
		}
		if got[6].Progress != nil || input[0].Progress != nil {
			t.Fatal("mutated file or input")
		}
		if c := got[3].Contents; c == nil || c.Videos != 1 || c.Images != 1 || c.Audio != 1 {
			t.Fatalf("b contents: %+v", c)
		}
		if c := got[4].Contents; c == nil || *c != (DirectoryContents{}) {
			t.Fatalf("empty contents: %+v", c)
		}
		return got
	}
	check(0, 0, 0)
	if r := l.SetProcessed(t.Context(), []int64{file.ID}, true); r.Changed != 1 || r.Failed != 0 {
		t.Fatal(r)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[1]',1,'running','2026-10-28T00:00:00.000Z','progress-test')")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO run_items(run,entry,identity,size,mtime_ns,status) VALUES(1,?,?,?,?, 'running')", file.ID, file.Identity, file.Size, file.ModifiedNS)
	}); err != nil {
		t.Fatal(err)
	}
	check(1, 1, 0)
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE runs SET status='completed'; UPDATE directory_state SET error='read failed' WHERE dir=?", nested.ID)
	}); err != nil {
		t.Fatal(err)
	}
	got := check(1, 0, 1)
	if got[2].Contents != nil {
		t.Fatal("failed directory has complete counts")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := l.WithProgressBatch(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request: %v", err)
	}
}

func TestProgressBatchDeepTreeAndCycle(t *testing.T) {
	l, _, _ := fixture(t)
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(2) UNION ALL SELECT x+1 FROM n WHERE x<500)
 INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) SELECT x,x-1,'folder','directory','d-'||x,0,0,0 FROM n;
 INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(501,500,'video.mp4','file','video',1,0,0)`)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := l.WithProgressBatch(t.Context(), []Entry{{ID: 1, Kind: "directory"}, {ID: 250, Kind: "directory"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Progress.Total != 1 || got[0].Progress.Unknown != 500 || got[1].Progress.Total != 1 || got[1].Progress.Unknown != 251 {
		t.Fatalf("deep tree: %+v %+v", got[0].Progress, got[1].Progress)
	}
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE entries SET parent=500 WHERE id=2")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.WithProgressBatch(t.Context(), []Entry{{ID: 2, Kind: "directory"}}); err == nil {
		t.Fatal("accepted directory cycle")
	}
}
