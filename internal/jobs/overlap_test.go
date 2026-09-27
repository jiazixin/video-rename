package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

func TestSubmissionDefersMediaPathValidation(t *testing.T) {
	for _, mode := range []string{"missing-file", "replaced-directory", "symlink", "missing-mount"} {
		t.Run(mode, func(t *testing.T) {
			m, ids := scopeFixture(t)
			root := m.Library.Root
			target := ids["direct.mp4"]
			switch mode {
			case "missing-file":
				if err := os.Remove(filepath.Join(root, "outer/direct.mp4")); err != nil {
					t.Fatal(err)
				}
			case "replaced-directory":
				target = ids["outer"]
				if err := os.Rename(filepath.Join(root, "outer"), filepath.Join(root, "old-outer")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "outer"), 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				path := filepath.Join(root, "outer/direct.mp4")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "alias.mp4"), path); err != nil {
					t.Fatal(err)
				}
			case "missing-mount":
				if err := os.Rename(root, root+"-unmounted"); err != nil {
					t.Fatal(err)
				}
			}
			id, err := m.Create(Request{Targets: []int64{ids["sibling"], target}, Recursive: true, Key: "defer-path-validation"}, "manual", 0)
			if err != nil {
				t.Fatal("submission accessed media paths", err)
			}
			m.Analyze = func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error) {
				t.Error("invalid path reached model processing")
				return "unexpected", nil
			}
			m.Execute(t.Context(), id)
			run, err := m.Get(id)
			if err != nil || run.Status != "failed" || run.Error == "" {
				t.Fatalf("invalid path not rejected by worker: %+v, err=%v", run, err)
			}
			var count int
			if err := m.DB.QueryRow("SELECT count(*) FROM run_items WHERE run=?", id).Scan(&count); err != nil || count != 0 {
				t.Fatal("validation must precede scanning every target", count, err)
			}
		})
	}
}

func scopeFixture(t *testing.T) (*Manager, map[string]int64) {
	t.Helper()
	m, root := setup(t)
	for _, dir := range []string{"outer/nested", "sibling"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"outer/direct.mp4", "outer/nested/deep.mp4", "sibling/other.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(root, "outer/direct.mp4"), filepath.Join(root, "alias.mp4")); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{"root": 1}
	for _, name := range []string{"root", "outer", "nested", "sibling"} {
		if name != "root" {
			var idsValue int64
			if err := m.DB.QueryRow("SELECT id FROM entries WHERE name=? AND present=1", name).Scan(&idsValue); err != nil {
				t.Fatal(err)
			}
			ids[name] = idsValue
		}
		if err := m.Library.Refresh(t.Context(), ids[name]); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"direct.mp4", "deep.mp4", "other.mp4", "alias.mp4"} {
		var id int64
		if err := m.DB.QueryRow("SELECT id FROM entries WHERE name=? AND present=1", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	return m, ids
}

func TestSubmissionNormalizesSelectedAncestors(t *testing.T) {
	for _, recursive := range []bool{true, false} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			m, ids := scopeFixture(t)
			targets := []int64{ids["deep.mp4"], ids["outer"], ids["nested"], ids["outer"]}
			original := slices.Clone(targets)
			id, err := m.Create(Request{Targets: targets, Recursive: recursive, Key: "normalize-targets"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(targets, original) {
				t.Fatal("submission changed caller's selection")
			}
			run, err := m.Get(id)
			want := []int64{ids["outer"]}
			if !recursive {
				want = []int64{ids["outer"], ids["nested"], ids["deep.mp4"]}
				slices.Sort(want)
			}
			if err != nil || !slices.Equal(run.Targets, want) {
				t.Fatal(run.Targets, want, err)
			}
			same, err := m.Create(Request{Targets: want, Recursive: recursive, Key: "normalize-targets"}, "manual", 0)
			if err != nil || same != id {
				t.Fatal("normalized retry lost idempotency", same, err)
			}
		})
	}
}

func TestSubmissionScopeOverlapSemantics(t *testing.T) {
	for _, tt := range []struct {
		name, first, second                      string
		firstRecursive, secondRecursive, overlap bool
	}{
		{"recursive ancestor", "outer", "deep.mp4", true, false, true},
		{"recursive parent submitted later", "deep.mp4", "outer", false, true, true},
		{"nonrecursive excludes grandchildren", "outer", "deep.mp4", false, false, false},
		{"nonrecursive includes direct file", "outer", "direct.mp4", false, false, true},
		{"nonrecursive parent submitted later", "direct.mp4", "outer", false, false, true},
		{"nonrecursive allows child directory", "outer", "nested", false, true, false},
		{"recursive excludes sibling", "outer", "sibling", true, true, false},
		{"hard links", "direct.mp4", "alias.mp4", false, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, ids := scopeFixture(t)
			if _, err := m.Create(Request{Targets: []int64{ids[tt.first]}, Recursive: tt.firstRecursive, Key: "first-scope-test"}, "manual", 0); err != nil {
				t.Fatal(err)
			}
			_, err := m.Create(Request{Targets: []int64{ids[tt.second]}, Recursive: tt.secondRecursive, Key: "second-scope-test"}, "manual", 0)
			if tt.overlap && (err == nil || !strings.Contains(err.Error(), "重叠")) || !tt.overlap && err != nil {
				t.Fatalf("overlap=%v, err=%v", tt.overlap, err)
			}
		})
	}
}

func TestQueuedScopeVisibleAndRejectsOtherBatch(t *testing.T) {
	m, root := setup(t)
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var a, b int64
	m.DB.QueryRow("SELECT id FROM entries WHERE name='a.mp4'").Scan(&a)
	m.DB.QueryRow("SELECT id FROM entries WHERE name='b.mp4'").Scan(&b)
	first, err := m.Create(Request{Targets: []int64{a}, Key: "first-running"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.DB.Exec("UPDATE runs SET status='running' WHERE id=?", first)
	second, err := m.Create(Request{Targets: []int64{b}, Key: "second-queued"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	e, err := m.Library.Get(b)
	if err != nil || e.TaskStatus != "queued" {
		t.Fatalf("queued state lost: %+v %v", e, err)
	}
	if _, err = m.Create(Request{Targets: []int64{b}, Key: "duplicate-file"}, "manual", 0); err == nil || !strings.Contains(err.Error(), "重叠") {
		t.Fatalf("duplicate allowed: %v", err)
	}
	if _, err = m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "duplicate-parent"}, "manual", 0); err == nil {
		t.Fatal("overlapping parent allowed")
	}
	if err = m.Cancel(second); err != nil {
		t.Fatal(err)
	}
	e, _ = m.Library.Get(b)
	if e.TaskStatus != "" {
		t.Fatal("cancel did not release scope")
	}
	var wg sync.WaitGroup
	successes := make(chan int64, 2)
	for _, key := range []string{"parallel-one", "parallel-two"} {
		wg.Go(func() {
			id, err := m.Create(Request{Targets: []int64{b}, Key: key}, "manual", 0)
			if err == nil {
				successes <- id
			}
		})
	}
	wg.Wait()
	close(successes)
	if len(successes) != 1 {
		t.Fatalf("concurrent duplicate batches: %d", len(successes))
	}
}

func TestQueuedDirectoryCoversIndexedChildren(t *testing.T) {
	m, root := setup(t)
	os.Mkdir(filepath.Join(root, "sub"), 0700)
	os.WriteFile(filepath.Join(root, "sub", "a.mp4"), []byte("video"), 0600)
	m.Library.Refresh(t.Context(), 1)
	var dir, file int64
	m.DB.QueryRow("SELECT id FROM entries WHERE name='sub'").Scan(&dir)
	m.Library.Refresh(t.Context(), dir)
	m.DB.QueryRow("SELECT id FROM entries WHERE name='a.mp4'").Scan(&file)
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: false, Key: "nonrecursive-root"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := m.Library.Get(file)
	if e.TaskStatus != "" {
		t.Fatal("nonrecursive scope leaked")
	}
	m.Cancel(id)
	_, err = m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "recursive-root"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []int64{1, dir, file} {
		e, err := m.Library.Get(entry)
		if err != nil || e.TaskStatus != "queued" {
			t.Fatalf("missing queued scope: %+v %v", e, err)
		}
	}
	if _, err = m.Create(Request{Targets: []int64{file}, Key: "covered-child"}, "manual", 0); err == nil {
		t.Fatal("covered child allowed")
	}
}
