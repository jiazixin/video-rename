package jobs

import (
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
				if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("folder-%03d", i)), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Library.Refresh(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			rows, err := m.DB.Query("SELECT id FROM entries WHERE parent=1 ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			var targets []int64
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				targets = append(targets, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil || len(targets) != 230 {
				t.Fatalf("targets=%d err=%v", len(targets), err)
			}
			request := Request{Task: task, Targets: targets, Recursive: true, Key: "multi-select-one-run"}
			id, err := m.Create(request, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			same, err := m.Create(request, "manual", 0)
			if err != nil || same != id {
				t.Fatalf("repeat=%d want=%d err=%v", same, id, err)
			}
			var runs, targetCount, items int
			if err := m.DB.QueryRow("SELECT count(*), json_array_length(targets) FROM runs").Scan(&runs, &targetCount); err != nil {
				t.Fatal(err)
			}
			if runs != 1 || targetCount != 230 {
				t.Fatalf("runs=%d targets=%d", runs, targetCount)
			}
			if err := m.DB.QueryRow("SELECT count(*) FROM run_items").Scan(&items); err != nil || items != 0 {
				t.Fatalf("submission scanned items=%d err=%v", items, err)
			}
			request.Key = "overlapping-selection"
			if _, err := m.Create(request, "manual", 0); err == nil {
				t.Fatal("overlapping active run accepted")
			}
		})
	}
}
