package libraryscan

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

func fixture(t *testing.T) (*Manager, *library.Library, string, systemsettings.Store) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "media")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(base, "state", "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := library.New(db, root)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	settings := systemsettings.Store{DB: db}
	m := New(l, settings)
	t.Cleanup(func() { m.Close(); l.Close(); db.Close() })
	return m, l, root, settings
}

func put(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func entryID(t *testing.T, l *library.Library, parent int64, name string) int64 {
	t.Helper()
	var id int64
	if err := l.DB.Read().QueryRow("SELECT id FROM entries WHERE parent=? AND name=? AND present=1", parent, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func await(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !predicate() {
		select {
		case <-deadline.C:
			t.Fatal("等待扫描状态超时")
		case <-tick.C:
		}
	}
}

func finished(t *testing.T, m *Manager) Status {
	t.Helper()
	await(t, func() bool {
		s := m.Snapshot()
		return s.Finished != "" && s.Status != "scanning" && s.Status != "cancelling"
	})
	return m.Snapshot()
}

func TestRecursiveDiscoveryFindsNewDirectoriesAndFiles(t *testing.T) {
	m, l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a", "one.mp4"))
	put(t, filepath.Join(root, "b", "nested", "two.mp4"))
	if _, err := m.Start(t.Context(), nil, "manual"); err != nil {
		t.Fatal(err)
	}
	s := finished(t, m)
	if s.Status != "completed" || s.Directories != 4 || s.Files != 2 || s.Added < 2 {
		t.Fatalf("initial scan: %+v", s)
	}
	a := entryID(t, l, 1, "a")
	b := entryID(t, l, 1, "b")
	entryID(t, l, a, "one.mp4")
	nested := entryID(t, l, b, "nested")
	entryID(t, l, nested, "two.mp4")
	put(t, filepath.Join(root, "b", "new", "three.mp4"))
	put(t, filepath.Join(root, "a", "four.mp4"))
	if _, err := m.Start(t.Context(), nil, "manual"); err != nil {
		t.Fatal(err)
	}
	s = finished(t, m)
	if s.Status != "completed" || s.Directories != 5 || s.Files != 4 || s.Added < 2 {
		t.Fatalf("second scan: %+v", s)
	}
	entryID(t, l, a, "four.mp4")
	newDir := entryID(t, l, b, "new")
	entryID(t, l, newDir, "three.mp4")
	var runs int
	if err := l.DB.Read().QueryRow("SELECT COUNT(*) FROM runs").Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("discovery created processing runs=%d err=%v", runs, err)
	}
}

func TestRecursiveDiscoveryTraversesMultipleChildPages(t *testing.T) {
	m, l, root, _ := fixture(t)
	const directories = 260
	for i := range directories {
		put(t, filepath.Join(root, fmt.Sprintf("folder-%03d", i), "one.mp4"))
	}
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	// Joining the bounded synthetic round also works under the race detector,
	// whose SQL instrumentation can exceed the short interactive-test timeout.
	m.wg.Wait()
	s := m.Snapshot()
	if s.Status != "completed" || s.Directories != directories+1 || s.Files != directories || len(s.Errors) != 0 {
		t.Fatalf("recursive child pages: %+v", s)
	}
	var files int
	if err := l.DB.Read().QueryRow("SELECT count(*) FROM entries WHERE kind='file' AND present=1").Scan(&files); err != nil || files != directories {
		t.Fatalf("recursive discovery skipped a child page: files=%d err=%v", files, err)
	}
	remaining, err := os.ReadDir(scratch)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("round stage was not removed: %v %v", remaining, err)
	}
}

func TestValidateTargetsNormalizesOverlapsAndRejectsInvalidScopes(t *testing.T) {
	m, l, root, _ := fixture(t)
	put(t, filepath.Join(root, "parent", "child", "one.mp4"))
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	finished(t, m)
	parent := entryID(t, l, 1, "parent")
	child := entryID(t, l, parent, "child")
	file := entryID(t, l, child, "one.mp4")
	targets, err := m.ValidateTargets(t.Context(), []int64{child, parent, child})
	if err != nil || !slices.Equal(targets, []int64{parent}) {
		t.Fatalf("normalized=%v err=%v", targets, err)
	}
	for _, invalid := range [][]int64{nil, {0}, {file}, {999999}, {parent, file}} {
		if _, err := m.ValidateTargets(t.Context(), invalid); err == nil {
			t.Fatalf("accepted invalid targets %v", invalid)
		}
	}
	if err := os.Rename(filepath.Join(root, "parent", "child"), filepath.Join(root, "parent", "old-child")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "parent", "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ValidateTargets(t.Context(), []int64{1, child}); err == nil {
		t.Fatal("covered child with changed identity was accepted")
	}
}

func TestCancelAndDuplicateStartsReuseOneRound(t *testing.T) {
	m, _, _, _ := fixture(t)
	entered := make(chan struct{})
	var calls atomic.Int64
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return library.ScanDirectoryResult{}, ctx.Err()
	}
	first, err := m.Start(t.Context(), []int64{1}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	duplicate, err := m.Start(t.Context(), []int64{999999}, "automatic")
	if err != nil || duplicate.ID != first.ID || duplicate.Source != "manual" || calls.Load() != 1 {
		t.Fatalf("duplicate=%+v err=%v calls=%d", duplicate, err, calls.Load())
	}
	if s := m.Cancel(); s.Status != "cancelling" || s.ID != first.ID {
		t.Fatalf("cancel=%+v", s)
	}
	if s := finished(t, m); s.Status != "cancelled" || len(s.Errors) != 0 {
		t.Fatalf("terminal=%+v", s)
	}
}

func TestDirectoryScanChangedRetriesOnceAndCountsOnlySuccessfulScan(t *testing.T) {
	m, l, root, _ := fixture(t)
	put(t, filepath.Join(root, "one.mp4"))
	put(t, filepath.Join(root, "two.mp4"))
	var calls atomic.Int64
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		if calls.Add(1) == 1 {
			return library.ScanDirectoryResult{Files: 100, Added: 100, Changed: 100, Removed: 100},
				fmt.Errorf("首次属性读取不一致：%w", library.ErrDirectoryScanChanged)
		}
		return l.ScanDirectory(ctx, id)
	}
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	s := finished(t, m)
	if calls.Load() != 2 || s.Status != "completed" || s.Directories != 1 || s.Files != 2 || s.Added != 2 || s.Changed != 0 || s.Removed != 0 || len(s.Errors) != 0 {
		t.Fatalf("retry did not publish/count exactly one successful scan: calls=%d status=%+v", calls.Load(), s)
	}
	entryID(t, l, 1, "one.mp4")
	entryID(t, l, 1, "two.mp4")
}

func TestDirectoryScanChangedPersistentFailureRetriesOnlyOnce(t *testing.T) {
	m, _, _, _ := fixture(t)
	var calls atomic.Int64
	m.scan = func(context.Context, int64) (library.ScanDirectoryResult, error) {
		attempt := calls.Add(1)
		return library.ScanDirectoryResult{Files: 100, Added: 100},
			fmt.Errorf("第 %d 次属性读取不一致：%w", attempt, library.ErrDirectoryScanChanged)
	}
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	s := finished(t, m)
	if calls.Load() != 2 || s.Status != "failed" || s.Directories != 0 || s.Files != 0 || s.Added != 0 || len(s.Errors) != 1 {
		t.Fatalf("persistent inconsistency was not bounded/reported once: calls=%d status=%+v", calls.Load(), s)
	}
	if s.Errors[0].Error.Code != "scan_changed" || !strings.Contains(s.Errors[0].Error.Message, "第 1 次属性读取不一致") || !strings.Contains(s.Errors[0].Error.Message, "第 2 次属性读取不一致") {
		t.Fatalf("retry failure lost an observation: %+v", s.Errors)
	}
}

func TestDirectoryScanChangedDoesNotRetryAfterCancellation(t *testing.T) {
	m, _, _, _ := fixture(t)
	entered, released := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	t.Cleanup(release)
	var calls atomic.Int64
	m.scan = func(context.Context, int64) (library.ScanDirectoryResult, error) {
		calls.Add(1)
		close(entered)
		<-released // The in-flight NAS call returns its own mismatch after cancellation.
		return library.ScanDirectoryResult{Files: 100}, library.ErrDirectoryScanChanged
	}
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	<-entered
	m.Cancel()
	release()
	s := finished(t, m)
	if calls.Load() != 1 || s.Status != "cancelled" || s.Directories != 0 || s.Files != 0 || len(s.Errors) != 0 {
		t.Fatalf("cancelled mismatch triggered more work: calls=%d status=%+v", calls.Load(), s)
	}
}

func TestDirectoryScanOrdinaryFailureDoesNotRetry(t *testing.T) {
	m, _, _, _ := fixture(t)
	var calls atomic.Int64
	wantErr := errors.New("NAS 目录读取失败")
	m.scan = func(context.Context, int64) (library.ScanDirectoryResult, error) {
		calls.Add(1)
		return library.ScanDirectoryResult{Files: 100}, wantErr
	}
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	s := finished(t, m)
	if calls.Load() != 1 || s.Status != "failed" || s.Directories != 0 || s.Files != 0 || len(s.Errors) != 1 || !strings.Contains(s.Errors[0].Error.Message, wantErr.Error()) {
		t.Fatalf("ordinary failure was retried or misreported: calls=%d status=%+v", calls.Load(), s)
	}
}

func TestCloseWaitsForInFlightFilesystemCallAndRejectsNewWork(t *testing.T) {
	m, _, _, _ := fixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		close(entered)
		<-release // Simulate a NAS syscall that cannot be interrupted.
		return library.ScanDirectoryResult{}, ctx.Err()
	}
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	<-entered
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	await(t, func() bool { return m.Snapshot().Status == "cancelling" })
	select {
	case <-closed:
		t.Fatal("Close returned before in-flight call completed")
	default:
	}
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); !errors.Is(err, ErrClosed) {
		t.Fatalf("new start after close: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	await(t, func() bool {
		select {
		case <-closed:
			return true
		default:
			return false
		}
	})
	if s := m.Snapshot(); s.Status != "cancelled" {
		t.Fatalf("terminal=%+v", s)
	}
}

func TestDirectoryFailurePreservesOldIndexAndContinuesIndependentRange(t *testing.T) {
	m, l, root, _ := fixture(t)
	put(t, filepath.Join(root, "bad", "old.mp4"))
	put(t, filepath.Join(root, "good", "old.mp4"))
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	finished(t, m)
	bad, good := entryID(t, l, 1, "bad"), entryID(t, l, 1, "good")
	put(t, filepath.Join(root, "bad", "new.mp4"))
	put(t, filepath.Join(root, "good", "new.mp4"))
	actual := m.library.ScanDirectory
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		if id == bad {
			return library.ScanDirectoryResult{}, errors.New("NAS 目录读取失败")
		}
		return actual(ctx, id)
	}
	if _, err := m.Start(t.Context(), []int64{bad, good}, "manual"); err != nil {
		t.Fatal(err)
	}
	s := finished(t, m)
	if s.Status != "partial_failure" || s.Directories != 1 || s.Files != 2 || len(s.Errors) != 1 || s.Errors[0].Path != "bad" {
		t.Fatalf("scan=%+v", s)
	}
	entryID(t, l, bad, "old.mp4")
	entryID(t, l, good, "new.mp4")
	var badNew int
	if err := l.DB.Read().QueryRow("SELECT COUNT(*) FROM entries WHERE parent=? AND name='new.mp4' AND present=1", bad).Scan(&badNew); err != nil || badNew != 0 {
		t.Fatalf("failed directory published new file: %d err=%v", badNew, err)
	}
}

func TestRootReplacementStopsRoundBeforeOtherDirectories(t *testing.T) {
	m, l, root, _ := fixture(t)
	put(t, filepath.Join(root, "one", "old.mp4"))
	put(t, filepath.Join(root, "two", "old.mp4"))
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	finished(t, m)
	one, two := entryID(t, l, 1, "one"), entryID(t, l, 1, "two")
	var calls atomic.Int64
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		calls.Add(1)
		if err := os.Rename(root, root+"-old"); err != nil {
			return library.ScanDirectoryResult{}, err
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			return library.ScanDirectoryResult{}, err
		}
		return library.ScanDirectoryResult{}, nil
	}
	if _, err := m.Start(t.Context(), []int64{one, two}, "manual"); err != nil {
		t.Fatal(err)
	}
	s := finished(t, m)
	if s.Status != "failed" || calls.Load() != 1 || len(s.Errors) != 1 || s.Errors[0].Path != library.RootDisplayPath {
		t.Fatalf("root change scan=%+v calls=%d", s, calls.Load())
	}
}

type fakeTimer struct {
	c       chan time.Time
	delay   time.Duration
	stopped atomic.Bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }
func (t *fakeTimer) Stop()               { t.stopped.Store(true) }

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	timer *fakeTimer
}

func (c *fakeClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }
func (c *fakeClock) NewTimer(d time.Duration) scanTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timer = &fakeTimer{c: make(chan time.Time, 1), delay: d}
	return c.timer
}
func (c *fakeClock) Timer() *fakeTimer { c.mu.Lock(); defer c.mu.Unlock(); return c.timer }

func TestAutomaticScheduleWaitsOneHourAndReplansFromCompletion(t *testing.T) {
	m, _, _, settings := fixture(t)
	clock := &fakeClock{now: time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)}
	m.now, m.newTimer = clock.Now, clock.NewTimer
	go m.Loop(t.Context())
	await(t, func() bool { return clock.Timer() != nil })
	if timer := clock.Timer(); timer.delay != time.Hour || m.Snapshot().Status != "idle" {
		t.Fatalf("startup timer=%s status=%+v", timer.delay, m.Snapshot())
	}
	clock.Advance(10 * time.Minute)
	config := systemsettings.DefaultScanSettings()
	config.IntervalMinutes = 120
	if err := settings.SaveScan(config); err != nil {
		t.Fatal(err)
	}
	m.SettingsChanged()
	await(t, func() bool {
		timer := clock.Timer()
		return timer != nil && timer.delay == 2*time.Hour && !timer.stopped.Load()
	})
	clock.Advance(2 * time.Hour)
	clock.Timer().c <- clock.Now()
	s := finished(t, m)
	if s.Source != "automatic" || s.Status != "completed" || s.Directories != 1 {
		t.Fatalf("automatic round=%+v", s)
	}
	await(t, func() bool { return m.Snapshot().NextScan == clock.Now().Add(2*time.Hour).Format(time.RFC3339Nano) })
	if timer := clock.Timer(); timer.delay != 2*time.Hour {
		t.Fatalf("completion timer=%s", timer.delay)
	}
	config.Enabled = false
	if err := settings.SaveScan(config); err != nil {
		t.Fatal(err)
	}
	m.SettingsChanged()
	await(t, func() bool { return m.Snapshot().NextScan == "" && clock.Timer().stopped.Load() })
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatalf("disabled automatic scanning blocked manual work: %v", err)
	}
	if s := finished(t, m); s.Source != "manual" || s.Status != "completed" {
		t.Fatalf("manual round=%+v", s)
	}
}

func TestLongManualRoundWaitsNewIntervalFromCompletion(t *testing.T) {
	m, _, _, settings := fixture(t)
	clock := &fakeClock{now: time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)}
	m.now, m.newTimer = clock.Now, clock.NewTimer
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	actual := m.library.ScanDirectory
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		close(entered)
		<-release
		return actual(ctx, id)
	}
	go m.Loop(t.Context())
	await(t, func() bool { return clock.Timer() != nil })
	clock.Advance(20 * time.Minute)
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	<-entered
	await(t, func() bool { return m.Snapshot().NextScan == "" && clock.Timer().stopped.Load() })
	config := systemsettings.DefaultScanSettings()
	config.IntervalMinutes = 90
	if err := settings.SaveScan(config); err != nil {
		t.Fatal(err)
	}
	m.SettingsChanged()
	clock.Advance(35 * time.Minute)
	releaseOnce.Do(func() { close(release) })
	if s := finished(t, m); s.Status != "completed" || s.Finished != clock.Now().Format(time.RFC3339Nano) {
		t.Fatalf("manual round=%+v", s)
	}
	await(t, func() bool {
		timer := clock.Timer()
		return m.Snapshot().NextScan == clock.Now().Add(90*time.Minute).Format(time.RFC3339Nano) &&
			timer != nil && timer.delay == 90*time.Minute && !timer.stopped.Load()
	})
}

func TestSettingsReadFailureIsVisibleAndDoesNotScan(t *testing.T) {
	m, l, _, settings := fixture(t)
	if _, err := l.DB.Exec("INSERT INTO system_settings(id,extract_workers,cache_gb,policy) VALUES(1,8,10,'invalid JSON')"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	actual := m.library.ScanDirectory
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		calls.Add(1)
		return actual(ctx, id)
	}
	go m.Loop(t.Context())
	await(t, func() bool { return len(m.Snapshot().Errors) == 1 })
	if s := m.Snapshot(); s.Status != "failed" || s.NextScan != "" || calls.Load() != 0 {
		t.Fatalf("invalid settings=%+v calls=%d", s, calls.Load())
	}
	if _, err := m.Start(t.Context(), nil, "manual"); err == nil {
		t.Fatal("manual default scope silently ignored settings failure")
	}
	if _, err := l.DB.Exec("UPDATE system_settings SET policy='' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := settings.SaveScan(systemsettings.DefaultScanSettings()); err != nil {
		t.Fatal(err)
	}
	m.SettingsChanged()
	await(t, func() bool { return len(m.Snapshot().Errors) == 0 && m.Snapshot().NextScan != "" })
}

func TestStatusSlicesAreIndependentAndNeverNull(t *testing.T) {
	m, _, _, _ := fixture(t)
	raw, err := json.Marshal(m.Snapshot())
	if err != nil || strings.Contains(string(raw), "null") {
		t.Fatalf("idle status JSON=%s err=%v", raw, err)
	}
	if _, err := m.Start(t.Context(), []int64{1}, "manual"); err != nil {
		t.Fatal(err)
	}
	s := finished(t, m)
	s.Targets[0] = 999999
	s.Errors = append(s.Errors, ScanError{Error: domain.Record{Message: "external mutation"}})
	if got := m.Snapshot(); got.Targets[0] != 1 || len(got.Errors) != 0 {
		t.Fatalf("Snapshot shared slices: %+v", got)
	}
}

func TestExcludedDirectoriesApplyToManualAndAutomaticScans(t *testing.T) {
	for _, source := range []string{"manual", "automatic"} {
		t.Run(source, func(t *testing.T) {
			m, l, root, store := fixture(t)
			put(t, filepath.Join(root, "skip", "nested", "old.mp4"))
			put(t, filepath.Join(root, "keep", "old.mp4"))
			if _, err := m.Start(t.Context(), nil, source); err != nil {
				t.Fatal(err)
			}
			finished(t, m)
			skip := entryID(t, l, 1, "skip")
			nested := entryID(t, l, skip, "nested")
			keep := entryID(t, l, 1, "keep")
			settings := systemsettings.DefaultScanSettings()
			settings.ExcludedTargets = []int64{skip}
			if err := store.SaveScan(settings); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(root, "skip", "new.mp4"))
			put(t, filepath.Join(root, "skip", "nested", "new.mp4"))
			put(t, filepath.Join(root, "keep", "new.mp4"))
			if _, err := m.Start(t.Context(), []int64{1}, source); err != nil {
				t.Fatal(err)
			}
			status := finished(t, m)
			if status.Status != "completed" || status.Directories != 2 {
				t.Fatalf("scan: %+v", status)
			}
			entryID(t, l, keep, "new.mp4")
			entryID(t, l, nested, "old.mp4") // Existing index is preserved.
			var count int
			if err := l.DB.Read().QueryRow("SELECT count(*) FROM entries WHERE parent IN (?,?) AND name='new.mp4'", skip, nested).Scan(&count); err != nil || count != 0 {
				t.Fatalf("excluded files indexed: %d %v", count, err)
			}
			for _, target := range []int64{skip, nested} {
				if _, err := m.Start(t.Context(), []int64{target}, source); err != nil {
					t.Fatal(err)
				}
				if status := finished(t, m); status.Status != "completed" || status.Directories != 0 {
					t.Fatalf("explicit excluded target: %+v", status)
				}
			}
			settings.ExcludedTargets = nil
			if err := store.SaveScan(settings); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Start(t.Context(), []int64{skip}, source); err != nil {
				t.Fatal(err)
			}
			if status := finished(t, m); status.Status != "completed" || status.Directories != 2 {
				t.Fatalf("removed exclusion: %+v", status)
			}
			entryID(t, l, nested, "new.mp4")
		})
	}
}

func TestScanExclusionsFrozenUntilNextRound(t *testing.T) {
	m, l, root, store := fixture(t)
	put(t, filepath.Join(root, "skip", "file.mp4"))
	if _, err := m.Start(t.Context(), nil, "manual"); err != nil {
		t.Fatal(err)
	}
	finished(t, m)
	skip := entryID(t, l, 1, "skip")
	entered, release := make(chan struct{}), make(chan struct{})
	m.scan = func(ctx context.Context, id int64) (library.ScanDirectoryResult, error) {
		if id == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return library.ScanDirectoryResult{}, ctx.Err()
			}
		}
		return library.ScanDirectoryResult{}, nil
	}
	if _, err := m.Start(t.Context(), nil, "manual"); err != nil {
		t.Fatal(err)
	}
	<-entered
	settings := systemsettings.DefaultScanSettings()
	settings.ExcludedTargets = []int64{skip}
	if err := store.SaveScan(settings); err != nil {
		t.Fatal(err)
	}
	m.SettingsChanged()
	close(release)
	if status := finished(t, m); status.Directories != 2 {
		t.Fatalf("active round changed: %+v", status)
	}
	m.scan = func(context.Context, int64) (library.ScanDirectoryResult, error) {
		return library.ScanDirectoryResult{}, nil
	}
	if _, err := m.Start(t.Context(), nil, "manual"); err != nil {
		t.Fatal(err)
	}
	if status := finished(t, m); status.Directories != 1 {
		t.Fatalf("next round ignored exclusion: %+v", status)
	}
}
