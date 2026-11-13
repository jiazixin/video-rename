package library

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/storage"
)

// Existing indexes isolate repeated scanning and validation from discovery.
// Real deployment callbacks and network filesystem latency are not simulated.
func BenchmarkProcessedMarks(b *testing.B) {
	for _, directories := range []int{1, 100} {
		b.Run(fmt.Sprintf("directories=%d", directories), func(b *testing.B) {
			base := b.TempDir()
			root := filepath.Join(base, "media")
			const filesPerDirectory = 53
			for d := range directories {
				dir := filepath.Join(root, fmt.Sprintf("dir-%04d", d))
				if err := os.MkdirAll(dir, 0o700); err != nil {
					b.Fatal(err)
				}
				for f := range filesPerDirectory {
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.mp4", f)), []byte("fixture"), 0o600); err != nil {
						b.Fatal(err)
					}
				}
			}
			db, err := storage.Open(b.Context(), filepath.Join(base, "state", "library.sqlite"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := db.Close(); err != nil {
					b.Error(err)
				}
			})
			l, err := New(b.Context(), db, root, slog.New(slog.DiscardHandler))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(l.Close)
			if result := l.SetProcessed(b.Context(), []int64{1}, true); result.Changed != int64(directories*filesPerDirectory) || result.Failed != 0 {
				b.Fatal(result)
			}
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				if err := db.Write(b.Context(), func(tx *sql.Tx) error {
					_, err := tx.ExecContext(b.Context(), "DELETE FROM marks")
					return err
				}); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				result := l.SetProcessed(b.Context(), []int64{1}, true)
				if result.Changed != int64(directories*filesPerDirectory) || result.Failed != 0 || result.Interrupted {
					b.Fatal(result)
				}
			}
		})
	}
}
