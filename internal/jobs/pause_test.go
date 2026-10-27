package jobs

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/systemsettings"
)

func waitPauseTest(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for batch state")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPauseReleasesQueuedFramesAndLetsOtherBatchFinish(t *testing.T) {
	m, root := setup(t)
	for _, dir := range []string{"first", "second"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		count := 1
		if dir == "first" {
			count = 4
		}
		for i := range count {
			if err := os.WriteFile(filepath.Join(root, dir, fmt.Sprintf("%d.mp4", i)), []byte("video"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var firstEntry, secondEntry int64
	m.DB.QueryRow("SELECT id FROM entries WHERE name='first'").Scan(&firstEntry)
	m.DB.QueryRow("SELECT id FROM entries WHERE name='second'").Scan(&secondEntry)
	profile, _ := (models.Store{DB: m.DB}).Get(1)
	profile.Workers = 1
	if _, err := (models.Store{DB: m.DB}).Save(profile); err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds, settings.ExtractWorkers = 0, 1
	if err := (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	reserve := int64(config.DefaultFrameSettings().Sampling.MaxFrames)*(1<<20) + (32 << 20) + 65536
	if err := m.Cache.Configure(reserve+65536+5, settings.Policy); err != nil {
		t.Fatal(err)
	}
	var extracted, inferred atomic.Int32
	entered, allow := make(chan struct{}), make(chan struct{})
	m.Prepare = func(_ context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		extracted.Add(1)
		return fakeFrames(work)
	}
	m.Infer = func(ctx context.Context, _ *config.TaskConfig, _ []domain.Frame) (string, error) {
		if inferred.Add(1) == 1 {
			close(entered)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-allow:
			}
		}
		return "renamed", nil
	}
	first, err := m.Create(t.Context(), Request{Targets: []int64{firstEntry}, Recursive: true, Key: "pause-first"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Create(t.Context(), Request{Targets: []int64{secondEntry}, Recursive: true, Key: "pause-second"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	wg.Go(func() { m.Execute(ctx, first) })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no model request")
	}
	waitPauseTest(t, func() bool { return extracted.Load() == 2 })
	if err = m.Pause(first); err != nil {
		t.Fatal(err)
	}
	if err = m.Pause(first); err != nil {
		t.Fatal("pause must be idempotent", err)
	}
	if _, err = m.Create(t.Context(), Request{Targets: []int64{firstEntry}, Recursive: true, Key: "pause-overlap"}, "manual", 0); err == nil {
		t.Fatal("paused scope lost overlap protection")
	}
	if _, err = m.Library.RestoreBatch(ctx, first); err == nil {
		t.Fatal("paused batch allowed restore")
	}
	wg.Go(func() { m.Execute(ctx, second) })
	close(allow)
	waitPauseTest(t, func() bool { r, _ := m.Get(t.Context(), second); return r.Status == "completed" })
	r, _ := m.Get(t.Context(), first)
	if r.Status != "paused" || r.Done != 1 || r.Failed != 0 || inferred.Load() != 2 {
		t.Fatalf("paused batch dispatched more work: %+v calls=%d", r, inferred.Load())
	}
	waitPauseTest(t, func() bool { return m.Cache.Stats().Reserved == 0 })
	page, err := m.ListPage(ctx, "all", "", 1, 50, url.Values{"status": {"paused"}})
	if err != nil || page.Total != 1 || page.Items[0].ID != first {
		t.Fatal(page, err)
	}
	directory, err := m.Library.Get(t.Context(), firstEntry)
	if err != nil || directory.TaskStatus != "paused" {
		t.Fatal(directory, err)
	}
	started := r.Started
	if err = m.Resume(first); err != nil {
		t.Fatal(err)
	}
	waitPauseTest(t, func() bool { r, _ := m.Get(t.Context(), first); return r.Status == "completed" })
	r, _ = m.Get(t.Context(), first)
	if r.Done != 4 || r.Failed != 0 || r.Started != started || inferred.Load() != 5 {
		t.Fatalf("resume repeated completed work: %+v calls=%d", r, inferred.Load())
	}
}

func TestQueuedPauseResumeCancelAndRecovery(t *testing.T) {
	m, root := setup(t)
	os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600)
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "queued-pause"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Pause(id); err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	r, _ := m.Get(t.Context(), id)
	if r.Status != "paused" || r.Started != "" {
		t.Fatal(r)
	}
	if err = m.Resume(id); err != nil {
		t.Fatal(err)
	}
	r, _ = m.Get(t.Context(), id)
	if r.Status != "queued" {
		t.Fatal(r)
	}
	m.Pause(id)
	if err = m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	r, _ = m.Get(t.Context(), id)
	if r.Status != "cancelled" || r.Finished == "" {
		t.Fatal(r)
	}
	if err = m.Resume(id); err == nil {
		t.Fatal("resumed a cancelled run")
	}
	if err = m.Pause(id); err == nil {
		t.Fatal("paused a cancelled run")
	}
	id, err = m.Create(t.Context(), Request{Targets: []int64{1}, Key: "paused-recovery"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Pause(id)
	if err = interruptRuns(m.DB); err != nil {
		t.Fatal(err)
	}
	r, _ = m.Get(t.Context(), id)
	if r.Status != "interrupted" || r.Finished == "" {
		t.Fatal(r)
	}
}

func TestPauseExtractionAndCancelWhilePaused(t *testing.T) {
	m, root := setup(t)
	for i := range 3 {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("%d.mp4", i)), []byte("video"), 0o600)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 0
	settings.ExtractWorkers = 1
	if err := (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	entered, allow := make(chan struct{}), make(chan struct{})
	m.Prepare = func(ctx context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-ctx.Done():
				return domain.FrameSet{}, ctx.Err()
			case <-allow:
			}
		}
		return fakeFrames(work)
	}
	id, err := m.Create(t.Context(), Request{Task: "extract", Targets: []int64{1}, Key: "extract-pause"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.Execute(ctx, id) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no extraction")
	}
	if err = m.Pause(id); err != nil {
		t.Fatal(err)
	}
	close(allow)
	waitPauseTest(t, func() bool { r, _ := m.Get(t.Context(), id); return r.Done == 1 })
	if err = m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	waitPauseTest(t, func() bool { r, _ := m.Get(t.Context(), id); return r.Status == "cancelled" })
	// Every extraction dispatched between the pause and the cancellation counts.
	if calls.Load() != 1 {
		t.Fatal("paused extraction kept dispatching")
	}
}

func TestPausePreservesRetrySequence(t *testing.T) {
	m, root := setup(t)
	os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600)
	var calls atomic.Int32
	entered, allow := make(chan struct{}), make(chan struct{})
	m.Analyze = func(ctx context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _ string) (string, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-allow:
			}
			return "", &domain.JobError{Code: domain.CodeNamingFailed, Message: "命名失败"}
		}
		return "renamed", nil
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "retry-pause"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.Execute(ctx, id) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no model")
	}
	m.Pause(id)
	close(allow)
	waitPauseTest(t, func() bool { var n int; m.DB.QueryRow("SELECT COUNT(*) FROM model_attempts").Scan(&n); return n == 1 })
	if calls.Load() != 1 {
		t.Fatal("retry ran while paused")
	}
	if err = m.Resume(id); err != nil {
		t.Fatal(err)
	}
	waitPauseTest(t, func() bool { r, _ := m.Get(t.Context(), id); return r.Status == "completed" })
	var n, maxAttempt int
	m.DB.QueryRow("SELECT COUNT(*),MAX(attempt) FROM model_attempts").Scan(&n, &maxAttempt)
	if n != 2 || maxAttempt != 2 || calls.Load() != 2 {
		t.Fatal(n, maxAttempt, calls.Load())
	}
}

func TestPauseDuringStabilityKeepsFrozenCandidates(t *testing.T) {
	m, root := setup(t)
	path := filepath.Join(root, "a.mp4")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := systemsettings.Default()
	settings.StabilitySeconds = 1
	if err := (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m.Analyze = func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error) {
		calls.Add(1)
		return "renamed", nil
	}
	waiting, stable := make(chan struct{}), make(chan time.Time)
	m.after = func(time.Duration) <-chan time.Time {
		close(waiting)
		return stable
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Key: "stability-pause"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.Execute(ctx, id) }()
	defer func() { cancel(); <-done }()
	<-waiting
	if err = m.Pause(id); err != nil {
		t.Fatal(err)
	}
	stable <- time.Now()
	// The stability wait ends by recording the running phase; the paused status must survive it.
	waitPauseTest(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		active, ok := m.active[id]
		return ok && active.control.phase == "running"
	})
	r, _ := m.Get(t.Context(), id)
	if r.Status != "paused" || calls.Load() != 0 {
		t.Fatal("stability completion overwrote pause", r, calls.Load())
	}
	if err = os.WriteFile(path, []byte("video changed while paused"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = m.Resume(id); err != nil {
		t.Fatal(err)
	}
	waitPauseTest(t, func() bool { r, _ := m.Get(t.Context(), id); return r.Status == "completed" })
	r, _ = m.Get(t.Context(), id)
	if r.Skipped != 1 || r.Done != 0 || calls.Load() != 0 {
		t.Fatal("resume bypassed source snapshot checks", r, calls.Load())
	}
}
