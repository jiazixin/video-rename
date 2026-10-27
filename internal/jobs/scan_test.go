package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/library"
)

func TestQueueFilesAtomicCountersAndDeduplication(t *testing.T) {
	for _, task := range []string{"rename", "extract"} {
		t.Run(task, func(t *testing.T) {
			m, root := setup(t)
			for _, name := range []string{"a.mp4", "b.mp4", "note.txt"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Link(filepath.Join(root, "a.mp4"), filepath.Join(root, "c.mp4")); err != nil {
				t.Fatal(err)
			}
			if err := m.Library.Refresh(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			page, err := m.Library.ListOrdered(t.Context(), 1, 0, 100, 0, "", "", "", false, "name_asc")
			if err != nil {
				t.Fatal(err)
			}
			var files []library.Entry
			for _, entry := range page.Items {
				e, err := m.Library.IndexedEntry(t.Context(), entry.ID)
				if err != nil {
					t.Fatal(err)
				}
				if e.Name == "a.mp4" || e.Name == "c.mp4" {
					e.Processed = "marked"
				}
				files = append(files, e)
			}
			id, err := m.Create(t.Context(), Request{Task: task, Targets: []int64{1}, Recursive: true, Key: "queue-atomic-test"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			batch, err := m.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			assert := func(want int) {
				t.Helper()
				var count int
				if err := m.DB.QueryRow("SELECT COUNT(*) FROM run_items WHERE run=?", id).Scan(&count); err != nil {
					t.Fatal(err)
				}
				r, err := m.Get(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				skipped := 0
				if want > 0 && task == "rename" {
					skipped = 1
				}
				if count != want || r.Discovered != want || r.Skipped != skipped {
					t.Fatalf("items=%d run=%+v want=%d skipped=%d", count, r, want, skipped)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := m.queueFiles(ctx, batch, files); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			assert(0)
			if _, err := m.DB.Exec("CREATE TRIGGER reject_candidate BEFORE INSERT ON run_items WHEN NEW.original_path='b.mp4' BEGIN SELECT RAISE(ABORT,'injected scan failure'); END"); err != nil {
				t.Fatal(err)
			}
			if err := m.queueFiles(t.Context(), batch, files); err == nil {
				t.Fatal("missing failure")
			}
			assert(0)
			if _, err := m.DB.Exec("DROP TRIGGER reject_candidate"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := m.queueFiles(t.Context(), batch, files); err != nil {
					t.Fatal(err)
				}
				assert(2)
			}
			var finished int64
			var status string
			if err := m.DB.QueryRow("SELECT status,finished_ms FROM run_items WHERE run=? AND original_path='a.mp4'", id).Scan(&status, &finished); err != nil {
				t.Fatal(err)
			}
			if task == "rename" && (status != "skipped" || finished == 0) || task == "extract" && (status != "queued" || finished != 0) {
				t.Fatalf("status=%s finished=%d", status, finished)
			}
		})
	}
}
