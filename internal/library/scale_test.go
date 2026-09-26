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
		p, err := l.List(t.Context(), dir.ID, cursor, 100, generation, "", "", false)
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
}
