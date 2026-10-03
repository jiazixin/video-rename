package library

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/storage"
)

// These local filesystem measurements include both metadata passes and durable
// publication. They isolate per-directory overhead; they are not NAS estimates.
func BenchmarkDirectoryScanUnchanged(b *testing.B) {
	for _, files := range []int{0, 40, 512} {
		for _, reuse := range []bool{false, true} {
			b.Run(fmt.Sprintf("files=%d/reuse=%v", files, reuse), func(b *testing.B) {
				base := b.TempDir()
				root := filepath.Join(base, "media")
				if err := os.Mkdir(root, 0700); err != nil {
					b.Fatal(err)
				}
				for i := range files {
					if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d.mp4", i)), []byte("video"), 0600); err != nil {
						b.Fatal(err)
					}
				}
				db, err := storage.Open(filepath.Join(base, "state", "library.sqlite"))
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { db.Close() })
				l, err := New(db, root)
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(l.Close)
				if _, err := l.ScanDirectory(b.Context(), 1); err != nil {
					b.Fatal(err)
				}
				scan := l.ScanDirectory
				if reuse {
					scanner, err := l.NewDirectoryScanner(b.Context())
					if err != nil {
						b.Fatal(err)
					}
					b.Cleanup(func() { scanner.Close() })
					scan = scanner.ScanDirectory
				}
				b.ReportAllocs()
				for b.Loop() {
					result, err := scan(b.Context(), 1)
					if err != nil || result.Files != int64(files) || result.Added+result.Changed+result.Removed != 0 {
						b.Fatalf("unchanged scan: %+v %v", result, err)
					}
				}
			})
		}
	}
}
