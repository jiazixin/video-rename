package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/systemsettings"
)

func fakeFrames(work string) (domain.FrameSet, error) {
	p := filepath.Join(work, "frame.jpg")
	err := os.WriteFile(p, []byte("frame"), 0600)
	return domain.FrameSet{Frames: []domain.Frame{{Path: p, Bytes: 5, Width: 1, Height: 1}}}, err
}
func TestStagedFailureAndCancellationReuseUntilCommit(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			m, root := setup(t)
			os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600)
			var extracts atomic.Int32
			m.Prepare = func(_ context.Context, _ *config.Config, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
				extracts.Add(1)
				return fakeFrames(work)
			}
			entered := make(chan struct{})
			m.Infer = func(ctx context.Context, _ *config.Config, frames []domain.Frame) (string, error) {
				if _, err := os.Stat(frames[0].Path); err != nil {
					t.Error(err)
				}
				close(entered)
				if cancelled {
					<-ctx.Done()
					return "", ctx.Err()
				}
				return "", fmt.Errorf("model unavailable")
			}
			id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "cache-first"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { defer close(done); m.Execute(t.Context(), id) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("model not reached")
			}
			if cancelled {
				if err = m.Cancel(id); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("run did not finish")
			}
			if m.Cache.Stats().Entries != 1 {
				t.Fatal("frames were not retained")
			}
			m.Infer = func(context.Context, *config.Config, []domain.Frame) (string, error) { return "renamed", nil }
			id, err = m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "cache-second"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			m.Execute(t.Context(), id)
			r, _ := m.Get(id)
			if r.Done != 1 || extracts.Load() != 1 || m.Cache.Stats().Entries != 0 {
				t.Fatal(r, extracts.Load(), m.Cache.Stats())
			}
		})
	}
}
func TestCrossBatchExtractionWhileModelBlockedAndSharedLimit(t *testing.T) {
	m, root := setup(t)
	for i := range 3 {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("%d.mp4", i)), []byte("video"), 0600)
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	rows, err := m.DB.Query("SELECT id FROM entries WHERE parent=1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var entries []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		entries = append(entries, id)
	}
	rows.Close()
	store := models.Store{DB: m.DB}
	profile, _ := store.Get(1)
	profile.Workers = 1
	if _, err = store.Save(profile); err != nil {
		t.Fatal(err)
	}
	if err = (systemsettings.Store{DB: m.DB}).Save(systemsettings.Settings{ExtractWorkers: 2, CacheGB: 1}); err != nil {
		t.Fatal(err)
	}
	var activeExtract, maxExtract, activeModel, maxModel atomic.Int32
	extracted := make(chan struct{}, 3)
	modelsStarted := make(chan struct{}, 3)
	releaseModel := make(chan struct{})
	m.Prepare = func(ctx context.Context, _ *config.Config, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		n := activeExtract.Add(1)
		for old := maxExtract.Load(); n > old && !maxExtract.CompareAndSwap(old, n); old = maxExtract.Load() {
		}
		defer activeExtract.Add(-1)
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return domain.FrameSet{}, ctx.Err()
		}
		set, err := fakeFrames(work)
		extracted <- struct{}{}
		return set, err
	}
	m.Infer = func(ctx context.Context, _ *config.Config, _ []domain.Frame) (string, error) {
		n := activeModel.Add(1)
		for old := maxModel.Load(); n > old && !maxModel.CompareAndSwap(old, n); old = maxModel.Load() {
		}
		defer activeModel.Add(-1)
		modelsStarted <- struct{}{}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-releaseModel:
			return "renamed", nil
		}
	}
	var runs []int64
	for _, entry := range entries {
		id, e := m.Create(Request{Targets: []int64{entry}, Key: fmt.Sprintf("batch-%08d", entry)}, "manual", 0)
		if e != nil {
			t.Fatal(e)
		}
		runs = append(runs, id)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.Loop(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("loop failed to stop")
		}
	}()
	for range 3 {
		select {
		case <-extracted:
		case <-time.After(5 * time.Second):
			t.Fatal("model wait blocked later batch extraction")
		}
	}
	select {
	case <-modelsStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no model started")
	}
	if maxExtract.Load() > 2 || maxExtract.Load() < 1 {
		t.Fatal("extract limit", maxExtract.Load())
	}
	select {
	case <-modelsStarted:
		t.Fatal("model limit multiplied across batches")
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseModel)
	deadline := time.Now().Add(5 * time.Second)
	for {
		complete := 0
		for _, id := range runs {
			r, _ := m.Get(id)
			if r.Status == "completed" {
				complete++
			}
		}
		if complete == len(runs) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runs not completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if maxModel.Load() != 1 || m.Cache.Stats().Entries != 0 {
		t.Fatal(maxModel.Load(), m.Cache.Stats())
	}
}

func TestCancelStopsExtractionAndLeavesOtherBatchRunning(t *testing.T) {
	m, root := setup(t)
	for _, name := range []string{"cancel.mp4", "continue.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	var cancelEntry, continueEntry int64
	if err := m.DB.QueryRow("SELECT id FROM entries WHERE name='cancel.mp4'").Scan(&cancelEntry); err != nil {
		t.Fatal(err)
	}
	if err := m.DB.QueryRow("SELECT id FROM entries WHERE name='continue.mp4'").Scan(&continueEntry); err != nil {
		t.Fatal(err)
	}
	first, e := m.Create(Request{Targets: []int64{cancelEntry}, Key: "cancel-extraction"}, "manual", 0)
	if e != nil {
		t.Fatal(e)
	}
	second, e := m.Create(Request{Targets: []int64{continueEntry}, Key: "continue-extraction"}, "manual", 0)
	if e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	stopped := make(chan struct{})
	modelStarted := make(chan struct{})
	release := make(chan struct{})
	m.Prepare = func(ctx context.Context, _ *config.Config, job domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		if filepath.Base(job.Path) == "cancel.mp4" {
			if _, e := fakeFrames(work); e != nil {
				return domain.FrameSet{}, e
			}
			close(started)
			<-ctx.Done()
			close(stopped)
			return domain.FrameSet{}, ctx.Err()
		}
		return fakeFrames(work)
	}
	m.Infer = func(ctx context.Context, _ *config.Config, _ []domain.Frame) (string, error) {
		close(modelStarted)
		select {
		case <-release:
			return "renamed", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(firstDone); m.Execute(ctx, first) }()
	go func() { defer close(secondDone); m.Execute(ctx, second) }()
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("stage timed out")
		}
	}
	wait(started)
	wait(modelStarted)
	if e = m.Cancel(first); e != nil {
		t.Fatal(e)
	}
	wait(stopped)
	wait(firstDone)
	select {
	case <-secondDone:
		t.Fatal("cancelling one run stopped another")
	default:
	}
	close(release)
	wait(secondDone)
	r, _ := m.Get(first)
	other, _ := m.Get(second)
	if r.Status != "cancelled" || other.Status != "completed" {
		t.Fatal(r, other)
	}
	if s := m.Cache.Stats(); s.Reserved != 0 || s.Entries != 0 {
		t.Fatal(s)
	}
}

func TestBatchPrefetchUsesCapacityInsteadOfVideoCount(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprintf("capacity-limited=%v", limited), func(t *testing.T) {
			m, root := setup(t)
			const count = 140 // Cross both the old 64-item cap and the 128-item SQL page.
			for i := range count {
				if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.mp4", i)), []byte("video"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			profile, err := (models.Store{DB: m.DB}).Get(1)
			if err != nil {
				t.Fatal(err)
			}
			profile.Workers = 1
			if _, err = (models.Store{DB: m.DB}).Save(profile); err != nil {
				t.Fatal(err)
			}
			settings := systemsettings.Default()
			settings.StabilitySeconds = 0
			settings.ExtractWorkers = 1
			if err = (systemsettings.Store{DB: m.DB}).Save(settings); err != nil {
				t.Fatal(err)
			}
			if limited {
				reserve := int64(config.DefaultFrameSettings().Sampling.MaxFrames)*(1<<20) + (32 << 20) + 65536
				// fakeFrames uses 5 bytes plus 64 KiB metadata: permit exactly three
				// pinned entries before the next peak extraction reservation must wait.
				if err = m.Cache.SetLimit(reserve + 2*(65536+5)); err != nil {
					t.Fatal(err)
				}
			}
			extracted := make(chan struct{}, count)
			permits := make(chan struct{}, count)
			m.Prepare = func(_ context.Context, _ *config.Config, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
				frames, err := fakeFrames(work)
				extracted <- struct{}{}
				return frames, err
			}
			m.Infer = func(ctx context.Context, _ *config.Config, frames []domain.Frame) (string, error) {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-permits:
				}
				if _, err := os.Stat(frames[0].Path); err != nil {
					return "", err
				}
				return "renamed", nil
			}
			id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "capacity-prefetch"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { defer close(done); m.Execute(ctx, id) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("cancellation did not drain pending work")
				}
			}()
			awaitFrame := func() {
				t.Helper()
				select {
				case <-extracted:
				case <-time.After(10 * time.Second):
					t.Fatal("prefetch stopped before expected capacity")
				}
			}
			expected := count
			if limited {
				expected = 3
			}
			for range expected {
				awaitFrame()
			}
			if limited {
				select {
				case <-extracted:
					t.Fatal("extracted beyond cache capacity")
				case <-time.After(150 * time.Millisecond):
				}
				if stats := m.Cache.Stats(); stats.Entries != 3 {
					t.Fatalf("waiting frames evicted: %+v", stats)
				}
				permits <- struct{}{} // A successful commit frees capacity for the next video.
				awaitFrame()
				select {
				case <-extracted:
					t.Fatal("capacity not enforced after refill")
				case <-time.After(150 * time.Millisecond):
				}
				if err = m.Cancel(id); err != nil {
					t.Fatal(err)
				}
			} else {
				r, err := m.Get(id)
				if err != nil || r.Done != 0 {
					t.Fatalf("expected prefetch while model blocked: %+v %v", r, err)
				}
				for range count {
					permits <- struct{}{}
				}
			}
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("run did not finish")
			}
			r, err := m.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if limited {
				if r.Status != "cancelled" || r.Done != 1 {
					t.Fatalf("cancelled run %+v", r)
				}
			} else if r.Done != count || r.Failed != 0 || m.Cache.Stats().Entries != 0 {
				t.Fatalf("completed run %+v cache %+v", r, m.Cache.Stats())
			}
		})
	}
}
