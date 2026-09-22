package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func TestProcessedMarksRecursiveAndPersistent(t *testing.T) {
	l, root, dbpath := fixture(t)
	if err := os.MkdirAll(filepath.Join(root, "dir", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "dir", "a.mp4"))
	put(t, filepath.Join(root, "dir", "nested", "b.mp4"))
	put(t, filepath.Join(root, "dir", "image.jpg"))
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "dir", "link")); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	d := entry(t, l, 1, "dir")
	r := l.SetProcessed(context.Background(), []int64{d.ID, d.ID}, true)
	if r.Changed != 2 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
	a := entry(t, l, d.ID, "a.mp4")
	if a.Processed != "marked" || a.RestoreID != 0 {
		t.Fatalf("%+v", a)
	}
	history, err := l.History(a.ID, 0)
	if err != nil || len(history) != 0 {
		t.Fatal(history, err)
	}
	put(t, filepath.Join(root, "dir", "new.mp4"))
	refresh(t, l, d.ID)
	if e := entry(t, l, d.ID, "new.mp4"); e.Processed != "" {
		t.Fatal("new file inherited mark")
	}
	l.DB.Close()
	db, err := storage.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err = New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	a, _ = l.Get(a.ID)
	if a.Processed != "marked" {
		t.Fatal("mark lost on restart")
	}
	r = l.SetProcessed(context.Background(), []int64{d.ID}, false)
	if r.Changed != 2 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestProcessedMarksPreserveHistoryAndValidateIdentity(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	a := entry(t, l, 1, "a.mp4")
	op, err := l.Rename(a.ID, a.Version, "b.mp4", "mark-history")
	if err != nil {
		t.Fatal(err)
	}
	r := l.SetProcessed(context.Background(), []int64{a.ID}, true)
	if r.Changed != 0 {
		t.Fatal("overwrote existing processed origin")
	}
	r = l.SetProcessed(context.Background(), []int64{a.ID}, false)
	if r.Changed != 1 {
		t.Fatalf("%+v", r)
	}
	a, _ = l.Get(a.ID)
	if a.Processed != "" || a.RestoreID != op.ID || a.Name != "b.mp4" {
		t.Fatalf("%+v", a)
	}
	if _, err = l.Restore(op.ID, "restore-after-unmark"); err != nil {
		t.Fatal(err)
	}
	l.Busy = func(int64) bool { return true }
	if r = l.SetProcessed(context.Background(), []int64{a.ID}, true); r.Failed != 1 {
		t.Fatal("busy file accepted")
	}
	l.Busy = nil
	if err = os.WriteFile(filepath.Join(root, "a.mp4"), []byte("different content"), 0600); err != nil {
		t.Fatal(err)
	}
	if r = l.SetProcessed(context.Background(), []int64{a.ID}, true); r.Failed != 1 {
		t.Fatal("stale file accepted")
	}
}

func TestRenameKeepsDirectoryPageAvailable(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	put(t, filepath.Join(root, "b.mp4"))
	refresh(t, l, 1)
	before, err := l.List(context.Background(), 1, 0, 100, 0, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	e := entry(t, l, 1, "a.mp4")
	if _, err = l.Rename(e.ID, e.Version, "z.mp4", "keep-index"); err != nil {
		t.Fatal(err)
	}
	after, err := l.List(context.Background(), 1, 0, 100, before.Generation, "", "", false)
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
	a := entry(t, l, 1, "a.mp4")
	alias := entry(t, l, 1, "alias.mp4")
	r := l.SetProcessed(context.Background(), []int64{a.ID, alias.ID, a.ID}, true)
	if r.Changed != 1 || r.Unchanged != 1 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
	alias, _ = l.Get(alias.ID)
	if alias.Processed != "marked" {
		t.Fatal("hard link did not share mark")
	}
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("new content of another size"), 0600); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	a, _ = l.Get(a.ID)
	if a.Processed != "" {
		t.Fatal("changed content inherited mark")
	}
}

func TestAutomaticSuccessDiagnosticsPersistWithHistory(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	refresh(t, l, 1)
	e := entry(t, l, 1, "a.mp4")
	snap, err := domain.StatSnapshot(filepath.Join(root, "a.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	op, err := l.AutoCommit(e.ID, snap, "new-name", "success-diagnostics", 0, domain.ModelDiagnostics{Request: `{"messages":[]}`, Response: `{"choices":[],"usage":10}`})
	if err != nil {
		t.Fatal(err)
	}
	var request, response string
	if err = l.DB.QueryRow("SELECT raw_request,raw_response FROM events WHERE operation=?", op.ID).Scan(&request, &response); err != nil || request == "" || response == "" {
		t.Fatal(request, response, err)
	}
}
