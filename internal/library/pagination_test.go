package library

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func TestListPageAfterRefreshAndRemoval(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 121 {
		put(t, filepath.Join(root, fmt.Sprintf("clip%d.mp4", i)))
	}
	refresh(t, l, 1)
	first, err := l.ListPage(t.Context(), 1, 2, 50, "", "video", "name_asc")
	if err != nil || first.Number != 2 || first.Items[0].Name != "clip50.mp4" {
		t.Fatalf("initial page: %+v, %v", first, err)
	}
	refresh(t, l, 1)
	after, err := l.ListPage(t.Context(), 1, 2, 50, "", "video", "name_asc")
	if err != nil || after.Generation != first.Generation || after.Number != 2 || after.Items[0].ID != first.Items[0].ID {
		t.Fatalf("refreshed page: %+v, %v", after, err)
	}
	if _, err := l.List(t.Context(), 1, first.Next, 50, first.Generation, "", "video", false); err != nil {
		t.Fatalf("unchanged refresh invalidated the cursor: %v", err)
	}
	for i := 60; i < 121; i++ {
		if err := os.Remove(filepath.Join(root, fmt.Sprintf("clip%d.mp4", i))); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	// Real listing changes must still invalidate cursors from the old generation.
	if _, err := l.List(t.Context(), 1, first.Next, 50, first.Generation, "", "video", false); err == nil {
		t.Fatal("stale cursor was accepted after removal")
	}
	for _, page := range []int{3, math.MaxInt} {
		p, err := l.ListPage(t.Context(), 1, page, 50, "", "video", "name_asc")
		if err != nil || p.Number != 2 || p.Total != 60 || len(p.Items) != 10 || p.Next != 0 {
			t.Fatalf("clamped page %d: %+v, %v", page, p, err)
		}
	}
	p, err := l.ListPage(t.Context(), 1, math.MaxInt, 50, "missing", "video", "name_asc")
	if err != nil || p.Number != 1 || p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("empty filter: %+v, %v", p, err)
	}
}

func TestListPageMatchesCursorSortingAndFilters(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 105 {
		put(t, filepath.Join(root, fmt.Sprintf("clip%d.mp4", i)))
		put(t, filepath.Join(root, fmt.Sprintf("photo%d.jpg", i)))
	}
	if err := os.Mkdir(filepath.Join(root, "clip-folder"), 0700); err != nil {
		t.Fatal(err)
	}
	refresh(t, l, 1)
	for _, sort := range []string{"name_asc", "name_desc", "size_asc", "size_desc", "mtime_asc", "mtime_desc", "renamed_asc", "renamed_desc"} {
		t.Run(sort, func(t *testing.T) {
			// Keep the static cursor comparison inside the index freshness window,
			// including under -race. Concurrent rebuilds are tested separately.
			refresh(t, l, 1)
			for _, filter := range []struct{ query, kind string }{{"", ""}, {"", "image"}, {"clip", "video"}} {
				cursor := 0
				var generation int64
				for page := 1; ; page++ {
					a, err := l.ListOrdered(t.Context(), 1, cursor, 50, generation, filter.query, filter.kind, false, sort)
					if err != nil {
						t.Fatal(err)
					}
					b, err := l.ListPage(t.Context(), 1, page, 50, filter.query, filter.kind, sort)
					if err != nil || b.Number != page || b.Total != a.Total || b.Next != a.Next || !slices.EqualFunc(a.Items, b.Items, func(a, b Entry) bool { return a.ID == b.ID }) {
						t.Fatalf("page %d filter %+v differs from cursor: %v", page, filter, err)
					}
					if a.Next == 0 {
						break
					}
					cursor, generation = a.Next, a.Generation
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
		p, err := l.ListPage(t.Context(), 1, 2, 50, "", "", "name_asc")
		if err != nil || p.Total != 130 || len(p.Items) != 50 || p.Items[0].Name != "clip50.mp4" || p.Items[49].Name != "clip99.mp4" {
			t.Fatalf("inconsistent page during rescan: %+v, %v", p, err)
		}
	}
}
