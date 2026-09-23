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

func TestDirectoryContentsUseLastCompleteDirectIndex(t *testing.T) {
	l, root, _ := fixture(t)
	directory := filepath.Join(root, "album")
	if err := os.MkdirAll(filepath.Join(directory, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clip.MP4", "photo.JPG", "notes.txt"} {
		put(t, filepath.Join(directory, name))
	}
	put(t, filepath.Join(directory, "nested", "deeper.mp4"))
	refresh(t, l, 1)
	album := entry(t, l, 1, "album")
	get := func() *DirectoryContents {
		t.Helper()
		e, err := l.WithProgress(album)
		if err != nil {
			t.Fatal(err)
		}
		return e.Contents
	}
	if c := get(); c != nil {
		t.Fatalf("unindexed directory has counts: %+v", c)
	}
	refresh(t, l, album.ID)
	if c := get(); c == nil || c.Directories != 1 || c.Videos != 1 || c.Images != 1 {
		t.Fatalf("direct counts: %+v", c)
	}
	put(t, filepath.Join(directory, "new.webp"))
	if c := get(); c == nil || c.Images != 1 {
		t.Fatalf("counts changed before refresh: %+v", c)
	}
	refresh(t, l, album.ID)
	if c := get(); c == nil || c.Images != 2 {
		t.Fatalf("refreshed counts: %+v", c)
	}
}
