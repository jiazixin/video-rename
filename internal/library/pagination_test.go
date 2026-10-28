package library

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestListPageAfterRefreshAndRemoval(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 121 {
		put(t, filepath.Join(root, fmt.Sprintf("clip%d.mp4", i)))
	}
	refresh(t, l, 1)
	first, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 50, Limit: 50, Query: "", Kind: "video", Status: "", SortBy: "name_asc"})
	if err != nil || first.Offset != first.Limit || first.Items[0].Name != "clip50.mp4" {
		t.Fatalf("initial page: %+v, %v", first, err)
	}
	refresh(t, l, 1)
	after, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 50, Limit: 50, Query: "", Kind: "video", Status: "", SortBy: "name_asc"})
	if err != nil || after.Generation != first.Generation || after.Offset != after.Limit || after.Items[0].ID != first.Items[0].ID {
		t.Fatalf("refreshed page: %+v, %v", after, err)
	}
	for i := 60; i < 121; i++ {
		if err := os.Remove(filepath.Join(root, fmt.Sprintf("clip%d.mp4", i))); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	for _, offset := range []int{100, math.MaxInt} {
		p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: offset, Limit: 50, Query: "", Kind: "video", Status: "", SortBy: "name_asc"})
		if err != nil || p.Offset != p.Limit || p.Total != 60 || len(p.Items) != 10 || p.Generation == first.Generation {
			t.Fatalf("clamped offset %d: %+v, %v", offset, p, err)
		}
	}
	p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: math.MaxInt, Limit: 50, Query: "missing", Kind: "video", Status: "", SortBy: "name_asc"})
	if err != nil || p.Offset != 0 || p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("empty filter: %+v, %v", p, err)
	}
}

func TestListPageSortingAndFilters(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 105 {
		put(t, filepath.Join(root, fmt.Sprintf("clip%d.mp4", i)))
		put(t, filepath.Join(root, fmt.Sprintf("photo%d.jpg", i)))
	}
	if err := os.Mkdir(filepath.Join(root, "clip-folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	for _, sort := range []string{"name_asc", "name_desc", "size_asc", "size_desc", "mtime_asc", "mtime_desc", "renamed_asc", "renamed_desc"} {
		t.Run(sort, func(t *testing.T) {
			refresh(t, l, 1)
			for _, filter := range []struct{ query, kind string }{{"", ""}, {"", "image"}, {"clip", "video"}} {
				want := map[string]bool{"clip-folder": true}
				for i := range 105 {
					if filter.kind != "image" {
						want[fmt.Sprintf("clip%d.mp4", i)] = true
					}
					if filter.kind != "video" {
						want[fmt.Sprintf("photo%d.jpg", i)] = true
					}
				}
				seen := map[string]bool{}
				for page := 1; ; page++ {
					p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: max(0, (page)-1) * (50), Limit: 50, Query: filter.query, Kind: filter.kind, SortBy: sort})
					if err != nil || p.Offset != (page-1)*p.Limit || p.Total != len(want) || len(p.Items) != min(50, len(want)-(page-1)*50) {
						t.Fatalf("got page %+v, error %v, want page %d and total %d", p, err, page, len(want))
					}
					if page == 1 && p.Items[0].Name != "clip-folder" {
						t.Fatal("directory did not sort first")
					}
					for _, item := range p.Items {
						if !want[item.Name] || seen[item.Name] {
							t.Fatalf("got unexpected or repeated item %q", item.Name)
						}
						seen[item.Name] = true
					}
					if page*50 >= p.Total {
						break
					}
				}
				if len(seen) != len(want) {
					t.Fatalf("got %d unique items, want %d", len(seen), len(want))
				}
			}
		})
	}
}

func TestListPageDuringRescans(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 130 {
		put(t, filepath.Join(root, fmt.Sprintf("clip%d.mp4", i)))
	}
	refresh(t, l, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 12 {
			if err := l.Refresh(t.Context(), 1); err != nil {
				t.Error(err)
				return
			}
		}
	})
	defer wg.Wait()
	for range 20 {
		p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 50, Limit: 50, Query: "", Kind: "", Status: "", SortBy: "name_asc"})
		if err != nil || p.Total != 130 || len(p.Items) != 50 || p.Items[0].Name != "clip50.mp4" || p.Items[49].Name != "clip99.mp4" {
			t.Fatalf("inconsistent page during rescan: %+v, %v", p, err)
		}
	}
}
