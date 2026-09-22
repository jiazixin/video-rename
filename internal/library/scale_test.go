//go:build scale

package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestScaleDirectoryPagination(t *testing.T) {
	l, root, _ := fixture(t)
	for d := 0; d < 5000; d++ {
		dir := filepath.Join(root, fmt.Sprintf("folder-%05d", d))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 10; f++ {
			put(t, filepath.Join(dir, fmt.Sprintf("clip-%02d.mp4", f)))
		}
	}
	flat := filepath.Join(root, "large-directory")
	if err := os.Mkdir(flat, 0700); err != nil {
		t.Fatal(err)
	}
	for f := 0; f < 50000; f++ {
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
	for i := 0; i < 30; i++ {
		start = time.Now()
		p, err := l.List(context.Background(), dir.ID, cursor, 100, generation, "", "", false)
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
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[28]
	t.Logf("cached 100-row page p95: %s (30 pages)", p95)
	if p95 > 300*time.Millisecond {
		t.Fatalf("P95 exceeds target: %s", p95)
	}
}
