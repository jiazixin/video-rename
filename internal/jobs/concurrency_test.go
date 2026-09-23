package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
)

func TestRunningBatchConcurrencyChangesAndCancellation(t *testing.T) {
	m, root := setup(t)
	for i := range 8 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%d.mp4", i)), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := models.Store{DB: m.DB}
	profile, err := store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	save := func(workers int) {
		t.Helper()
		profile.Workers = workers
		var err error
		profile, err = store.Save(profile)
		if err != nil {
			t.Fatal(err)
		}
	}
	save(1)
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "dynamic-workers"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan int, 8)
	release := make(chan struct{})
	m.Analyze = func(ctx context.Context, cfg *config.Config, j domain.VideoJob, _, _ string) (string, error) {
		if cfg.Server.Model != "test-model" {
			t.Error("request settings changed")
		}
		started <- cfg.Workers
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "renamed-" + filepath.Base(j.Path), nil
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.Execute(ctx, id) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("batch did not stop")
		}
	}()
	next := func(want int) {
		t.Helper()
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("workers=%d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no task started")
		}
	}
	noStart := func() {
		t.Helper()
		select {
		case got := <-started:
			t.Fatalf("unexpected dispatch with workers=%d", got)
		case <-time.After(650 * time.Millisecond):
		}
	}
	finishOne := func() {
		t.Helper()
		select {
		case release <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("no task to release")
		}
	}
	next(1)
	// Increase while the first request is still blocked: no completion required.
	profile.Model = "changed-model"
	save(3)
	next(3)
	next(3)
	save(1)
	finishOne()
	noStart()
	finishOne()
	noStart()
	finishOne()
	next(1)
	if err := m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel timed out")
	}
	select {
	case <-started:
		t.Fatal("queued task dispatched after cancellation")
	default:
	}
	run, err := m.Get(id)
	if err != nil || run.Status != "cancelled" || run.Done != 3 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
}

func TestDeletedModelUsesBatchConcurrencySnapshot(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	store := models.Store{DB: m.DB}
	profile, err := store.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	profile.Workers = 3
	profile, err = store.Save(profile)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "deleted-workers"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.DB.Exec("DELETE FROM model_configs WHERE id=?", profile.ID); err != nil {
		t.Fatal(err)
	}
	m.Analyze = func(_ context.Context, cfg *config.Config, _ domain.VideoJob, _, _ string) (string, error) {
		if cfg.Workers != 3 {
			t.Errorf("workers=%d", cfg.Workers)
		}
		return "renamed", nil
	}
	m.Execute(t.Context(), id)
	run, err := m.Get(id)
	if err != nil || run.Status != "completed" || run.Done != 1 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
}
