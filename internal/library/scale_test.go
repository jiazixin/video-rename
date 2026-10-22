//go:build scale

package library

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestScaleDirectoryPagination(t *testing.T) {
	l, root, _ := fixture(t)
	for d := range 5000 {
		dir := filepath.Join(root, fmt.Sprintf("folder-%05d", d))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for f := range 10 {
			put(t, filepath.Join(dir, fmt.Sprintf("clip-%02d.mp4", f)))
		}
	}
	flat := filepath.Join(root, "large-directory")
	if err := os.Mkdir(flat, 0700); err != nil {
		t.Fatal(err)
	}
	for f := range 50000 {
		put(t, filepath.Join(flat, fmt.Sprintf("clip-%05d.mp4", f)))
	}
	start := time.Now()
	refresh(t, l, 1)
	t.Logf("5,001 direct directories index: %s", time.Since(start))
	dir := entry(t, l, 1, "large-directory")
	start = time.Now()
	refresh(t, l, dir.ID)
	t.Logf("50,000 direct files index: %s", time.Since(start))
	durations := []time.Duration{}
	cursor := 0
	var generation int64
	for range 30 {
		start = time.Now()
		p, err := l.ListOrdered(t.Context(), dir.ID, cursor, 100, generation, "", "", "", false, "name_asc")
		if err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(start))
		if len(p.Items) != 100 {
			t.Fatal("unexpected page size")
		}
		cursor = p.Next
		generation = p.Generation
	}
	slices.Sort(durations)
	p95 := durations[28]
	t.Logf("cached 100-row page p95: %s (30 pages)", p95)
	if p95 > 300*time.Millisecond {
		t.Fatalf("P95 exceeds target: %s", p95)
	}
	for _, page := range []int{1, 250, 500} {
		start = time.Now()
		p, err := l.ListPage(t.Context(), dir.ID, page, 100, "", "video", "", "name_asc")
		elapsed := time.Since(start)
		t.Logf("50,000-file numbered page %d: %s", page, elapsed)
		if err != nil || p.Number != page || p.Total != 50000 || len(p.Items) != 100 || p.Items[0].Name != fmt.Sprintf("clip-%05d.mp4", (page-1)*100) {
			t.Fatalf("unexpected numbered page %d: %v", page, err)
		}
		if elapsed > 300*time.Millisecond {
			t.Fatalf("numbered page %d exceeds target: %s", page, elapsed)
		}
	}
}

func TestScaleProcessedMarks(t *testing.T) {
	l, root, _ := fixture(t)
	for d := range 2 {
		dir := filepath.Join(root, fmt.Sprintf("mark-%d", d))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for f := range 10000 {
			put(t, filepath.Join(dir, fmt.Sprintf("clip-%05d.mp4", f)))
		}
	}
	refresh(t, l, 1)
	targets := []int64{entry(t, l, 1, "mark-0").ID, entry(t, l, 1, "mark-1").ID}
	for _, marked := range []bool{true, true, false} {
		start := time.Now()
		result := l.SetProcessed(t.Context(), targets, marked)
		t.Logf("marked=%v: %+v, elapsed=%s", marked, result, time.Since(start))
		if result.Failed != 0 || result.Changed+result.Unchanged != 20000 {
			t.Fatalf("incomplete marking: %+v", result)
		}
		var count int
		if err := l.DB.QueryRow("SELECT count(*) FROM marks").Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 0
		if marked {
			want = 20000
		}
		if count != want {
			t.Fatalf("marks=%d, want %d", count, want)
		}
	}
}
