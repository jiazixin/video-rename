package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestQueuedScopeVisibleAndRejectsOtherBatch(t *testing.T) {
	m, root := setup(t)
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Library.Refresh(context.Background(), 1); err != nil {
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
	if err = m.Library.Refresh(context.Background(), 1); err != nil {
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
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			id, err := m.Create(Request{Targets: []int64{b}, Key: key}, "manual", 0)
			if err == nil {
				successes <- id
			}
		}(key)
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
	m.Library.Refresh(context.Background(), 1)
	var dir, file int64
	m.DB.QueryRow("SELECT id FROM entries WHERE name='sub'").Scan(&dir)
	m.Library.Refresh(context.Background(), dir)
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
