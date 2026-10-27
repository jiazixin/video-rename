package libraryscan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/library"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

// Compare identical local trees with per-directory stages and one reused round
// stage. Publication retains the real WAL/FULL settings in both cases.
func BenchmarkRecursiveDirectoryScan(b *testing.B) {
	const directories, filesPerDirectory = 128, 40
	for _, initial := range []bool{true, false} {
		for _, reuse := range []bool{false, true} {
			b.Run(fmt.Sprintf("initial=%v/reuse=%v", initial, reuse), func(b *testing.B) {
				base := b.TempDir()
				root := filepath.Join(base, "media")
				for i := range directories {
					dir := filepath.Join(root, fmt.Sprintf("folder-%03d", i))
					if err := os.MkdirAll(dir, 0o700); err != nil {
						b.Fatal(err)
					}
					for j := range filesPerDirectory {
						if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d.mp4", j)), []byte("video"), 0o600); err != nil {
							b.Fatal(err)
						}
					}
				}
				newManager := func() (*Manager, *storage.Store) {
					db, err := storage.Open(b.Context(), filepath.Join(base, "state", "library.sqlite"))
					if err != nil {
						b.Fatal(err)
					}
					l, err := library.New(b.Context(), db, root)
					if err != nil {
						b.Fatal(err)
					}
					m := New(l, systemsettings.Store{DB: db})
					if !reuse {
						m.scan = l.ScanDirectory
					}
					return m, db
				}
				m, db := newManager()
				finish := func() {
					m.Close()
					m.library.Close()
					db.Close()
				}
				b.Cleanup(func() { finish() })
				run := func() {
					m.status = Status{Status: "scanning", Targets: []int64{1}, Errors: []ScanError{}}
					m.cancel = func() {}
					m.run(b.Context(), []int64{1}, nil)
					if m.status.Status != "completed" || m.status.Directories != directories+1 || m.status.Files != directories*filesPerDirectory {
						b.Fatalf("scan status: %+v", m.status)
					}
				}
				if !initial {
					run()
				}
				b.ReportAllocs()
				for b.Loop() {
					run()
					if initial {
						b.StopTimer()
						finish()
						if err := os.RemoveAll(filepath.Join(base, "state")); err != nil {
							b.Fatal(err)
						}
						m, db = newManager()
						b.StartTimer()
					}
				}
			})
		}
	}
}
