//go:build scale

package library

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/domain"
)

func TestScaleNumberingTree(t *testing.T) {
	l, root, _ := fixture(t)
	const directories, files = 100, 200
	for d := range directories {
		dir := filepath.Join(root, fmt.Sprintf("group-%02d", d/10), fmt.Sprintf("child-%03d", d), "deep")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for i := range files {
			put(t, filepath.Join(dir, fmt.Sprintf("%d.JPG", i)))
		}
	}
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = true
	start := time.Now()
	batches, err := l.PreviewNumberingTree(t.Context(), 1, settings)
	if err != nil || len(batches) != directories {
		t.Fatalf("batches=%d err=%v", len(batches), err)
	}
	total := 0
	for _, batch := range batches {
		if batch.Total != files {
			t.Fatalf("%s: %d files", batch.Path, batch.Total)
		}
		for i, item := range batch.Items {
			if item.Old != fmt.Sprintf("%d.JPG", i) || item.New != fmt.Sprintf("%d.jpg", i+1) {
				t.Fatalf("%s: %+v", batch.Path, item)
			}
		}
		total += batch.Total
	}
	t.Logf("%d files in %d media directories across 3 levels: preview %s", total, directories, time.Since(start))
}
