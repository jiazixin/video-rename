package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryProgressRequiresCompleteIndex(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "a.mp4"))
	put(t, filepath.Join(root, "nested", "b.mp4"))
	refresh(t, l, 1)
	get := func() *DirectoryProgress {
		e, err := l.Get(1)
		if err != nil {
			t.Fatal(err)
		}
		e, err = l.WithProgress(e)
		if err != nil {
			t.Fatal(err)
		}
		return e.Progress
	}
	if p := get(); p.Unknown != 1 || p.Total != 1 {
		t.Fatalf("%+v", p)
	}
	a := entry(t, l, 1, "a.mp4")
	l.SetProcessed(context.Background(), []int64{a.ID}, true)
	if p := get(); p.Unknown != 1 || p.Processed != 1 {
		t.Fatalf("%+v", p)
	}
	d := entry(t, l, 1, "nested")
	refresh(t, l, d.ID)
	if p := get(); p.Unknown != 0 || p.Total != 2 || p.Processed != 1 {
		t.Fatalf("%+v", p)
	}
	l.SetProcessed(context.Background(), []int64{d.ID}, true)
	if p := get(); p.Processed != 2 || p.Total != 2 {
		t.Fatalf("%+v", p)
	}
	l.SetProcessed(context.Background(), []int64{1}, false)
	if p := get(); p.Processed != 0 || p.Total != 2 {
		t.Fatalf("%+v", p)
	}
}
