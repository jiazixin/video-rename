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
	"video-rename/internal/systemsettings"
)

func batchTargets(t *testing.T, m *Manager, root string, n int) []int64 {
	t.Helper()
	for i := range n {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("clip-%03d.mp4", i)), []byte("video"), 0o600); err != nil {
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
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestWaitingBatchCapacityAndPauseClassification(t *testing.T) {
	m, root := setup(t)
	targets := batchTargets(t, m, root, 27)
	settings := systemsettings.Default()
	settings.WaitingBatchLimit = 25
	store := systemsettings.Store{DB: m.DB}
	if err := store.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 25 {
		id, err := m.Create(t.Context(), Request{Targets: []int64{targets[i]}, Key: fmt.Sprintf("capacity-%d", i)}, "manual", 0)
		if err != nil {
			t.Fatalf("batch %d: %v", i+1, err)
		}
		ids = append(ids, id)
	}
	if err := m.Pause(t.Context(), ids[0]); err != nil {
		t.Fatal(err)
	}
	request := Request{Targets: []int64{targets[25]}, Key: "capacity-extra"}
	if _, err := m.Create(t.Context(), request, "manual", 0); err == nil {
		t.Fatal("unstarted paused batch did not count toward waiting capacity")
	}
	// A paused batch that has started belongs to the execution side.
	if _, err := m.DB.Exec("UPDATE runs SET started='now' WHERE id=?", ids[0]); err != nil {
		t.Fatal(err)
	}
	extra, err := m.Create(t.Context(), request, "manual", 0)
	if err != nil {
		t.Fatal("started paused batch consumed waiting capacity", err)
	}
	// Lowering the limit preserves the queue and request-key idempotency.
	settings.WaitingBatchLimit = 1
	if err := store.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	same, err := m.Create(t.Context(), request, "manual", 0)
	if err != nil || same != extra {
		t.Fatal("queue limit broke idempotency", same, err)
	}
	if _, err := m.Create(t.Context(), Request{Targets: []int64{targets[26]}, Key: "capacity-new"}, "schedule", 1); err == nil {
		t.Fatal("new submission accepted over lowered capacity")
	}
	if err := m.Cancel(t.Context(), extra); err != nil {
		t.Fatal(err)
	}
	settings.WaitingBatchLimit = 25
	if err := store.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(t.Context(), Request{Targets: []int64{targets[26]}, Key: "capacity-new"}, "manual", 0); err != nil {
		t.Fatal("cancellation did not free waiting capacity", err)
	}
}

func TestLoopDynamicBatchCapacityAndShutdown(t *testing.T) {
	m, root := setup(t)
	targets := batchTargets(t, m, root, 35)
	store := systemsettings.Store{DB: m.DB}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	settings.RunningBatchLimit = 1
	settings.ExtractWorkers = 32
	if err := store.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	profile, err := (models.Store{DB: m.DB}).Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	profile.Workers = 32
	if _, err := (models.Store{DB: m.DB}).Save(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 35)
	m.Analyze = func(ctx context.Context, _ *config.TaskConfig, job domain.VideoJob, _, _ string) (string, error) {
		started <- job.Path
		<-ctx.Done()
		return "", ctx.Err()
	}
	var ids []int64
	for i, target := range targets {
		id, err := m.Create(t.Context(), Request{Targets: []int64{target}, Key: fmt.Sprintf("dispatch-%d", i)}, "manual", 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := m.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("loop shutdown blocked")
		}
	}()
	next := func(n int) string {
		t.Helper()
		var last string
		for range n {
			select {
			case last = <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("batch did not start")
			}
		}
		return last
	}
	noStart := func() {
		t.Helper()
		select {
		case <-started:
			t.Fatal("dispatch exceeded running limit")
		case <-time.After(1200 * time.Millisecond):
		}
	}
	if got := next(1); got != filepath.Join(root, "clip-000.mp4") {
		t.Fatal("oldest batch was not started first", got)
	}
	noStart()
	// Started pauses retain their execution slot.
	if err := m.Pause(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	noStart()
	if err := m.Resume(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if got := next(1); got != filepath.Join(root, "clip-001.mp4") {
		t.Fatal("cancellation did not start next queued batch", got)
	}
	noStart()
	settings.RunningBatchLimit = 32
	if err := store.Save(ctx, settings); err != nil {
		t.Fatal(err)
	}
	next(31)
	noStart()
	settings.RunningBatchLimit = 1
	if err := store.Save(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	noStart()
	// Cancelling the loop with more than the old channel capacity must finish.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expanded loop failed to stop")
	}
}
