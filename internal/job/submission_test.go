package job

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMultiSelectCreatesOneRun(t *testing.T) {
	for _, task := range []string{"rename", "extract"} {
		t.Run(task, func(t *testing.T) {
			m, root := setup(t)
			for i := range 230 {
				if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("folder-%03d", i)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Library.Refresh(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			targets := indexedIDs(t, m.db)
			if len(targets) != 230 {
				t.Fatal(len(targets))
			}
			request := Request{Task: task, Targets: targets, Recursive: true, Key: "multi-select-one-run"}
			id, err := m.Create(t.Context(), request, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			same, err := m.Create(t.Context(), request, "manual", 0)
			if err != nil || same != id {
				t.Fatalf("repeat=%d want=%d err=%v", same, id, err)
			}
			var runs, targetCount, items int
			if err := m.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT count(*), json_array_length(targets) FROM runs").Scan(&runs, &targetCount)
			}); err != nil {
				t.Fatal(err)
			}
			if runs != 1 || targetCount != 230 {
				t.Fatalf("runs=%d targets=%d", runs, targetCount)
			}
			if err := m.db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM run_items").Scan(&items)
			}); err != nil || items != 0 {
				t.Fatalf("submission scanned items=%d err=%v", items, err)
			}
			request.Key = "overlapping-selection"
			if _, err := m.Create(t.Context(), request, "manual", 0); err == nil {
				t.Fatal("overlapping active run accepted")
			}
		})
	}
}
