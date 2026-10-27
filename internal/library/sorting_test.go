package library

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNaturalSortingAcrossPages(t *testing.T) {
	l, root, _ := fixture(t)
	for i := 120; i > 0; i-- {
		put(t, filepath.Join(root, fmt.Sprintf("视频%d.mp4", i)))
	}
	if err := os.Mkdir(filepath.Join(root, "目录10"), 0o700); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	for _, sortBy := range []string{"name_asc", "name_desc"} {
		desc := sortBy == "name_desc"
		cursor := 0
		var generation int64
		var files []string
		for {
			p, err := l.ListOrdered(t.Context(), 1, cursor, 50, generation, "", "", "", false, sortBy)
			if err != nil {
				t.Fatal(err)
			}
			if p.Total != 121 {
				t.Fatalf("total = %d", p.Total)
			}
			if cursor == 0 && p.Items[0].Kind != "directory" {
				t.Fatal("directory must stay first")
			}
			for _, e := range p.Items {
				if e.Kind == "file" {
					files = append(files, e.Name)
				}
			}
			if p.Next == 0 {
				break
			}
			cursor = p.Next
			generation = p.Generation
		}
		if len(files) != 120 {
			t.Fatalf("got %d files", len(files))
		}
		for i, name := range files {
			n := i + 1
			if desc {
				n = 120 - i
			}
			if name != fmt.Sprintf("视频%d.mp4", n) {
				t.Fatalf("position %d: %s", i, name)
			}
		}
	}
}

func TestMetadataSortingAndRenameTime(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"2.mp4", "10.mp4", "1.mp4"} {
		put(t, filepath.Join(root, name))
	}
	refresh(t, l, 1)
	for i, name := range []string{"2.mp4", "10.mp4", "1.mp4"} {
		_, err := l.DB.Exec("UPDATE directory_items SET size=?,mtime=? WHERE name=?", i+1, fmt.Sprintf("2026-09-%02dT00:00:00Z", i+1), name)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"size", "mtime"} {
		for _, direction := range []string{"asc", "desc"} {
			p, err := l.ListOrdered(t.Context(), 1, 0, 50, 0, "", "", "", false, field+"_"+direction)
			if err != nil {
				t.Fatal(err)
			}
			expected := "2.mp4"
			if direction == "desc" {
				expected = "1.mp4"
			}
			if p.Items[0].Name != expected {
				t.Fatalf("%s %s: %+v", field, direction, p.Items)
			}
		}
	}
	before := entry(t, l, 1, "2.mp4")
	if before.RenamedAt != "" {
		t.Fatal("unexpected rename time")
	}
	op, err := l.Rename(t.Context(), before.ID, before.Version, "3.mp4", "sort-rename")
	if err != nil {
		t.Fatal(err)
	}
	after, err := l.Get(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.RenamedAt != op.Created || after.Mtime != before.Mtime {
		t.Fatalf("bad timestamps: %+v", after)
	}
	p, err := l.ListOrdered(t.Context(), 1, 0, 50, 0, "", "", "", false, "renamed_desc")
	if err != nil {
		t.Fatal(err)
	}
	if p.Items[0].ID != before.ID {
		t.Fatal("latest rename not first")
	}
}

func TestDirectoryStatusSorting(t *testing.T) {
	l, root, _ := fixture(t)
	// Names deliberately interleave completed, partial, and running directories.
	groups := [][]string{{"dir2", "dir5"}, {"dir3", "dir6"}, {"dir1", "dir4"}}
	for _, names := range groups {
		for _, name := range names {
			if err := os.MkdirAll(filepath.Join(root, name, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(root, name, "nested", "a.mp4"))
			put(t, filepath.Join(root, name, "nested", "b.mp4"))
		}
	}
	refresh(t, l, 1)
	for group, names := range groups {
		for _, name := range names {
			dir := entry(t, l, 1, name)
			refresh(t, l, dir.ID)
			nested := entry(t, l, dir.ID, "nested")
			refresh(t, l, nested.ID)
			setTestProcessingSource(t, l, nested.ID, "a.mp4", "manual")
			if group != 1 {
				setTestProcessingSource(t, l, nested.ID, "b.mp4", "automatic")
			}
			if group == 0 {
				if _, err := l.DB.Exec(`INSERT INTO runs(source,targets,recursive,status,created,request_key) VALUES('manual',?,1,'running','now',?)`, fmt.Sprintf("[%d]", dir.ID), name); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		p, err := l.ListPage(t.Context(), 1, 1, 50, "", "", "", "status_"+direction)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"dir2", "dir5", "dir3", "dir6", "dir1", "dir4"}
		if direction == "desc" {
			slices.Reverse(want)
		}
		var got []string
		for _, e := range p.Items {
			got = append(got, e.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", direction, got, want)
		}
	}
}
