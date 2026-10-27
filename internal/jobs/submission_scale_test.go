//go:build scale

package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScaleMultiSelectSubmission(t *testing.T) {
	m, root := setup(t)
	for d := range 200 {
		dir := filepath.Join(root, fmt.Sprintf("folder-%03d", d))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("video-%03d.mp4", d)), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"file", "directory"} {
		rows, err := m.DB.Query("SELECT id FROM entries WHERE parent=1 AND kind=? ORDER BY id", kind)
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
		if err != nil || len(targets) != 200 {
			t.Fatal(len(targets), err)
		}
		var created []int64
		for batch := range 2 {
			start := time.Now()
			id, err := m.Create(t.Context(), Request{Targets: targets[batch*100 : (batch+1)*100], Recursive: true, Key: fmt.Sprintf("submission-%s-%d", kind, batch)}, "manual", 0)
			t.Logf("%s: 100 targets, existing batches=%d, submission=%s", kind, batch, time.Since(start))
			if err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Fatalf("submission exceeded 3s: %s", elapsed)
			}
			created = append(created, id)
		}
		for _, id := range created {
			if err := m.Cancel(id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestScaleMixedDirectorySubmission(t *testing.T) {
	m, root := setup(t)
	for d := range 3 {
		dir := filepath.Join(root, fmt.Sprintf("large-%d", d))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for f := range 3000 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("video-%04d.mp4", f)), []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for f := range 2 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("selected-%d.mp4", f)), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var targets []int64
	for _, name := range []string{"large-0", "large-1", "large-2", "selected-0.mp4", "selected-1.mp4"} {
		var id int64
		if err := m.DB.QueryRow("SELECT id FROM entries WHERE parent=1 AND name=?", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, id)
	}
	for _, indexed := range []bool{false, true} {
		if indexed {
			for _, id := range targets[:3] {
				if err := m.Library.Refresh(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, selection := range [][]int64{{1}, targets} {
			start := time.Now()
			id, err := m.Create(t.Context(), Request{Targets: selection, Recursive: true, Key: fmt.Sprintf("mixed-%v-%d", indexed, len(selection))}, "manual", 0)
			elapsed := time.Since(start)
			t.Logf("9,002 videos, indexed=%v, targets=%d, submission=%s", indexed, len(selection), elapsed)
			if err != nil {
				t.Fatal(err)
			}
			if elapsed > 3*time.Second {
				t.Fatal("submission exceeded 3s", elapsed)
			}
			var count int
			if err := m.DB.QueryRow("SELECT count(*) FROM run_items WHERE run=?", id).Scan(&count); err != nil || count != 0 {
				t.Fatalf("submission performed worker scan: items=%d, err=%v", count, err)
			}
			if err := m.Cancel(id); err != nil {
				t.Fatal(err)
			}
		}
	}
}
