package framecache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/systemsettings"
)

func build(work string) (domain.FrameSet, error) {
	p := filepath.Join(work, "frame.jpg")
	err := os.WriteFile(p, []byte("frame"), 0600)
	return domain.FrameSet{Frames: []domain.Frame{{Path: p, Bytes: 5, Width: 1, Height: 1}}}, err
}
func mustCache(t *testing.T, root string, limit int64) *Cache {
	t.Helper()
	c, e := New(root, limit)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func TestReuseRestartCorruptionAndSuccessfulRemoval(t *testing.T) {
	base := t.TempDir()
	c := mustCache(t, base, 1000000)
	key := strings.Repeat("a", 64)
	lease, err := c.Acquire(t.Context(), key, 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	path := lease.Set.Frames[0].Path
	lease.Release(false)
	c.Close()
	c = mustCache(t, base, 1000000)
	unexpected := func(string) (domain.FrameSet, error) {
		t.Fatal("complete cache was not reused")
		return domain.FrameSet{}, nil
	}
	lease, err = c.Acquire(t.Context(), key, 100000, unexpected)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release(false)
	// Same length corruption must not be reused.
	if err = os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	lease, err = c.Acquire(t.Context(), key, 100000, func(p string) (domain.FrameSet, error) { calls++; return build(p) })
	if err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	lease.Release(true)
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("successful frames retained", err)
	}
}
func TestCapacityWaitCancellationEvictionAndPinnedExpiry(t *testing.T) {
	c := mustCache(t, t.TempDir(), 140000)
	a, err := c.Acquire(t.Context(), strings.Repeat("a", 64), 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = c.Acquire(ctx, strings.Repeat("b", 64), 100000, build)
	if err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if err = c.Sweep(time.Now().Add(4 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c.Stats().Entries != 1 {
		t.Fatal("pinned cache expired")
	}
	a.Release(false)
	b, err := c.Acquire(t.Context(), strings.Repeat("b", 64), 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(a.Set.Frames[0].Path); !os.IsNotExist(err) {
		t.Fatal("idle cache not evicted")
	}
	b.Release(false)
	if err = c.Sweep(time.Now().Add(4 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c.Stats().Entries != 0 {
		t.Fatal("idle cache not expired")
	}
}
func TestPartialFailureCleanedAndUnknownFilesUntouched(t *testing.T) {
	base := t.TempDir()
	sentinel := filepath.Join(base, "personal.txt")
	os.WriteFile(sentinel, []byte("keep"), 0600)
	c := mustCache(t, base, 1000000)
	_, err := c.Acquire(t.Context(), strings.Repeat("a", 64), 100000, func(work string) (domain.FrameSet, error) {
		build(work)
		return domain.FrameSet{}, fmt.Errorf("cancelled extraction")
	})
	if err == nil || c.Stats().Reserved != 0 || c.Stats().Entries != 0 {
		t.Fatal(err, c.Stats())
	}
	files, _ := os.ReadDir(c.root)
	if len(files) != 1 || files[0].Name() != ".lock" {
		t.Fatal(files)
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "keep" {
		t.Fatal("unrelated file changed")
	}
	if _, err = New(base, 1000000); err == nil {
		t.Fatal("second process cache accepted")
	}
}
func TestMappingDependsOnSourceAndFramesNotModel(t *testing.T) {
	frame := config.DefaultFrameSettings()
	cfg := config.TaskConfig{Sampling: frame.Sampling, Images: frame.Images}
	job := domain.VideoJob{Snapshot: domain.Snapshot{Dev: 1, Ino: 2, Size: 3, ModTime: time.Unix(1000, 0)}}
	key := Key(job, &cfg)
	cfg.Prompt = "new prompt"
	cfg.Server.Model = "new model"
	job.Path = "new/path"
	if Key(job, &cfg) != key {
		t.Fatal("model or path changed frame key")
	}
	job.Snapshot.Size++
	if Key(job, &cfg) == key {
		t.Fatal("source changes ignored")
	}
	job.Snapshot.Size--
	cfg.Images.MaxDimension++
	if Key(job, &cfg) == key {
		t.Fatal("frame parameters ignored")
	}
}

func TestSharedLeaseSuccessDeletesAfterLastReader(t *testing.T) {
	c := mustCache(t, t.TempDir(), 1000000)
	key := strings.Repeat("c", 64)
	first, err := c.Acquire(t.Context(), key, 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Acquire(t.Context(), key, 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	first.Release(true)
	if _, err = os.Stat(second.Set.Frames[0].Path); err != nil {
		t.Fatal("removed another reader's frames", err)
	}
	second.Release(false)
	if c.Stats().Entries != 0 {
		t.Fatal("committed frames retained after last reader")
	}
}

func TestRetentionPoliciesAndRestart(t *testing.T) {
	for _, mode := range []string{"immediate", "delayed", "keep"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			policy := systemsettings.Default().Policy
			policy.SuccessCacheMode, policy.SuccessCacheHours, policy.IdleCacheHours = mode, 120, 0
			c, err := New(base, 1000000, policy)
			if err != nil {
				t.Fatal(err)
			}
			key := strings.Repeat("c", 64)
			lease, err := c.Acquire(t.Context(), key, 100000, build)
			if err != nil {
				t.Fatal(err)
			}
			lease.Release(true)
			if mode == "immediate" {
				if c.Stats().Entries != 0 {
					t.Fatal("immediate retained")
				}
				c.Close()
				return
			}
			c.mu.Lock()
			c.entries[key].LastUsed = time.Now().Add(-96 * time.Hour)
			if err = c.persist(c.entries[key]); err != nil {
				t.Fatal(err)
			}
			c.mu.Unlock()
			c.Close()
			c, err = New(base, 1000000, policy)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if c.Stats().Entries != 1 {
				t.Fatal("startup used fixed 3-day retention")
			}
			if err = c.Sweep(time.Now().Add(121 * time.Hour)); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "delayed" {
				want = 0
			}
			if c.Stats().Entries != want {
				t.Fatal(c.Stats())
			}
		})
	}
}
func TestClearProtectsLeasesAndBuilders(t *testing.T) {
	c := mustCache(t, t.TempDir(), 1000000)
	a, err := c.Acquire(t.Context(), strings.Repeat("a", 64), 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Acquire(t.Context(), strings.Repeat("b", 64), 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	b.Release(false)
	c.mu.Lock()
	c.building["pending-test"] = true
	c.building[a.key] = true
	c.mu.Unlock()
	result, err := c.Clear()
	if err != nil || result.Deleted != 1 || result.Retained != 2 || result.FreedBytes <= 0 {
		t.Fatal(result, err)
	}
	if _, err = os.Stat(a.Set.Frames[0].Path); err != nil {
		t.Fatal(err)
	}
	a.Release(false)
	c.mu.Lock()
	delete(c.building, "pending-test")
	delete(c.building, a.key)
	c.mu.Unlock()
	result, err = c.Clear()
	if err != nil || result.Deleted != 1 || result.Retained != 0 {
		t.Fatal(result, err)
	}
}
func TestIdleAndSuccessExpiryWhicheverFirst(t *testing.T) {
	c := mustCache(t, t.TempDir(), 1000000)
	policy := systemsettings.Default().Policy
	policy.SuccessCacheMode = "delayed"
	policy.SuccessCacheHours = 168
	if err := c.Configure(1000000, policy); err != nil {
		t.Fatal(err)
	}
	lease, err := c.Acquire(t.Context(), strings.Repeat("d", 64), 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release(true)
	if err = c.Sweep(time.Now().Add(73 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c.Stats().Entries != 0 {
		t.Fatal("idle deadline did not win")
	}
}
