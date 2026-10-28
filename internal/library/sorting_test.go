package library

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"video-rename/internal/storage"
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
		var files []string
		for page := 1; ; page++ {
			p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: max(0, (page)-1) * (50), Limit: 50, Query: "", Kind: "", Status: "", SortBy: sortBy})
			if err != nil {
				t.Fatal(err)
			}
			if p.Total != 121 {
				t.Fatalf("total = %d", p.Total)
			}
			if page == 1 && p.Items[0].Kind != "directory" {
				t.Fatal("directory must stay first")
			}
			for _, e := range p.Items {
				if e.Kind == "file" {
					files = append(files, e.Name)
				}
			}
			if page*50 >= p.Total {
				break
			}
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
		_, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), "UPDATE directory_items SET size=?,mtime_ns=? WHERE name=?", i+1, time.Date(2026, time.September, i+1, 0, 0, 0, 0, time.UTC).UnixNano(), name)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"size", "mtime"} {
		for _, direction := range []string{"asc", "desc"} {
			p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 50, Query: "", Kind: "", Status: "", SortBy: field + "_" + direction})
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
	before := entry(t, entryOptions{l: l, parent: 1, name: "2.mp4"})
	if before.RenamedAt != "" {
		t.Fatal("unexpected rename time")
	}
	op, err := l.Rename(t.Context(), RenameRequest{ID: before.ID, Version: before.Version, Name: "3.mp4", Key: "sort-rename"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := l.Get(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.RenamedAt != op.Created || after.ModifiedNS != before.ModifiedNS {
		t.Fatalf("bad timestamps: %+v", after)
	}
	p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 50, Query: "", Kind: "", Status: "", SortBy: "renamed_desc"})
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
			dir := entry(t, entryOptions{l: l, parent: 1, name: name})
			refresh(t, l, dir.ID)
			nested := entry(t, entryOptions{l: l, parent: dir.ID, name: "nested"})
			refresh(t, l, nested.ID)
			setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: nested.ID, name: "a.mp4", source: "manual"})
			if group != 1 {
				setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: nested.ID, name: "b.mp4", source: "automatic"})
			}
			if group == 0 {
				if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
					return tx.ExecContext(t.Context(), `INSERT INTO runs(source,targets,recursive,status,created,request_key) VALUES('manual',?,1,'running','2026-10-28T00:00:00.000Z',?)`, fmt.Sprintf("[%d]", dir.ID), name)
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 50, Query: "", Kind: "", Status: "", SortBy: "status_" + direction})
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
