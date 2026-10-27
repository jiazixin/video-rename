package framecache

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/systemsettings"
)

func statsTestCache(t *testing.T, base string) *Cache {
	t.Helper()
	policy := systemsettings.Default().Policy
	policy.SuccessCacheMode, policy.IdleCacheHours = "keep", 0
	c, err := New(base, 1000000, slog.New(slog.DiscardHandler), policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	return c
}

func statsTestAcquire(t *testing.T, c *Cache, key string) *Lease {
	t.Helper()
	lease, err := c.Acquire(t.Context(), key, 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func assertStatsAccounted(t *testing.T, c *Cache) Stats {
	t.Helper()
	c.mu.Lock()
	want := Stats{Path: c.root, Reserved: c.reserved, Entries: len(c.entries)}
	for _, r := range c.entries {
		want.Used += r.Bytes
	}
	c.mu.Unlock()
	got := c.Stats()
	if got != want {
		t.Fatalf("stats=%+v, entry accounting=%+v", got, want)
	}
	return got
}

// Release the operation lock before draining an old, blocking Stats call, so
// this regression fails without leaving a goroutine or a locked cache behind.
func assertStatsWhileLocked(t *testing.T, c *Cache, want Stats) {
	t.Helper()
	c.mu.Lock()
	done := make(chan Stats, 1)
	go func() { done <- c.Stats() }()
	select {
	case got := <-done:
		c.mu.Unlock()
		if got != want {
			t.Fatalf("stats while locked=%+v, want %+v", got, want)
		}
	case <-time.After(500 * time.Millisecond):
		c.mu.Unlock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Stats did not return after releasing the operation lock")
		}
		t.Fatal("Stats waited for the operation lock")
	}
}

func TestStatsDoesNotWaitForCacheOperations(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%t", populated), func(t *testing.T) {
			c := statsTestCache(t, t.TempDir())
			if populated {
				lease := statsTestAcquire(t, c, strings.Repeat("a", 64))
				defer lease.Release(false)
			}
			want := assertStatsAccounted(t, c)
			assertStatsWhileLocked(t, c, want)
		})
	}
}

func TestStatsTracksBuildReservationsAndRestart(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fail), func(t *testing.T) {
			base := t.TempDir()
			c := statsTestCache(t, base)
			existing := statsTestAcquire(t, c, strings.Repeat("a", 64))
			existing.Release(false)
			before := assertStatsAccounted(t, c)
			started, resume := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(resume) })
			type result struct {
				lease *Lease
				err   error
			}
			done := make(chan result, 1)
			consumed := false
			buildFailure := errors.New("synthetic extraction failure")
			t.Cleanup(func() {
				unblock()
				if !consumed {
					select {
					case r := <-done:
						if r.lease != nil {
							r.lease.Release(false)
						}
					case <-time.After(2 * time.Second):
						t.Error("blocked Acquire did not finish during cleanup")
					}
				}
			})
			go func() {
				lease, err := c.Acquire(t.Context(), strings.Repeat("b", 64), 100000, func(work string) (domain.FrameSet, error) {
					close(started)
					select {
					case <-resume:
					case <-t.Context().Done():
						return domain.FrameSet{}, t.Context().Err()
					}
					set, err := build(work)
					if err == nil && fail {
						err = buildFailure
					}
					return set, err
				})
				done <- result{lease, err}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("Acquire did not enter build")
			}
			reserved := before
			reserved.Reserved = 100000
			assertStatsWhileLocked(t, c, reserved)
			if got := assertStatsAccounted(t, c); got != reserved {
				t.Fatalf("during build=%+v, want %+v", got, reserved)
			}
			unblock()
			var completed result
			select {
			case completed = <-done:
				consumed = true
			case <-time.After(2 * time.Second):
				t.Fatal("Acquire did not finish")
			}
			if fail {
				if !errors.Is(completed.err, buildFailure) || completed.lease != nil {
					t.Fatalf("failed build result=%+v", completed)
				}
			} else {
				if completed.err != nil || completed.lease == nil {
					t.Fatalf("successful build result=%+v", completed)
				}
				completed.lease.Release(false)
			}
			after := assertStatsAccounted(t, c)
			wantEntries := 2
			if fail {
				wantEntries = 1
				if after != before {
					t.Fatalf("failed build changed committed stats: before=%+v after=%+v", before, after)
				}
			}
			if after.Reserved != 0 || after.Entries != wantEntries || after.Used <= 0 {
				t.Fatalf("after build=%+v", after)
			}
			dirs, err := os.ReadDir(c.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range dirs {
				if strings.HasPrefix(d.Name(), "pending-") {
					t.Fatalf("partial workspace retained: %s", d.Name())
				}
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := statsTestCache(t, base)
			restarted := assertStatsAccounted(t, reopened)
			if restarted.Entries != wantEntries || restarted.Reserved != 0 || restarted.Used < after.Used {
				t.Fatalf("restart stats=%+v, before close=%+v", restarted, after)
			}
			lease, err := reopened.Acquire(t.Context(), strings.Repeat("a", 64), 100000, func(string) (domain.FrameSet, error) {
				return domain.FrameSet{}, errors.New("cached frames should be reused")
			})
			if err != nil {
				t.Fatal(err)
			}
			lease.Release(false)
			assertStatsAccounted(t, reopened)
		})
	}
}

func TestStatsTracksRevalidatedBytes(t *testing.T) {
	c := statsTestCache(t, t.TempDir())
	key := strings.Repeat("a", 64)
	lease := statsTestAcquire(t, c, key)
	lease.Release(false)
	dir := filepath.Join(c.root, key)
	manifest := filepath.Join(dir, "manifest.json")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, append(raw, []byte(strings.Repeat(" ", 2048))...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	var diskBytes int64
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil {
			diskBytes += info.Size()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	lease, err = c.Acquire(t.Context(), key, 100000, func(string) (domain.FrameSet, error) {
		return domain.FrameSet{}, errors.New("valid cached frames should be reused")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release(false)
	if got := assertStatsAccounted(t, c); got.Used != diskBytes+65536 || got.Entries != 1 {
		t.Fatalf("revalidated stats=%+v, disk bytes=%d", got, diskBytes)
	}
}

func TestStatsAccountsRejectedValidationWithoutRemovingLease(t *testing.T) {
	c := statsTestCache(t, t.TempDir())
	key := strings.Repeat("a", 64)
	lease := statsTestAcquire(t, c, key)
	defer lease.Release(false)
	dir := filepath.Join(c.root, key)
	if err := os.WriteFile(filepath.Join(dir, "0-unrelated.txt"), []byte(strings.Repeat("x", 30)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a-unknown-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := c.Acquire(t.Context(), key, 100000, build)
	if err == nil || other != nil {
		t.Fatal("cache with an unknown subdirectory was accepted")
	}
	if got := assertStatsAccounted(t, c); got.Entries != 1 || got.Used != 65536+30 {
		t.Fatalf("partially revalidated stats=%+v", got)
	}
	if _, err := os.Stat(lease.Set.Frames[0].Path); err != nil {
		t.Fatal("validation removed an active lease", err)
	}
}

func TestStatsDeletionProtectsActiveLeases(t *testing.T) {
	c := statsTestCache(t, t.TempDir())
	active := statsTestAcquire(t, c, strings.Repeat("a", 64))
	defer active.Release(false)
	idle := statsTestAcquire(t, c, strings.Repeat("b", 64))
	idle.Release(false)
	result, err := c.Clear()
	if err != nil || result.Deleted != 1 || result.Retained != 1 || result.FreedBytes <= 0 {
		t.Fatal(result, err)
	}
	if got := assertStatsAccounted(t, c); got.Entries != 1 || got.Used <= 0 {
		t.Fatal(got)
	}
	if _, err := os.Stat(active.Set.Frames[0].Path); err != nil {
		t.Fatal("Clear removed active frames", err)
	}
	if _, err := os.Stat(idle.Set.Frames[0].Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Clear retained idle frames", err)
	}
	policy := systemsettings.Default().Policy
	policy.SuccessCacheMode = "immediate"
	if err := c.Configure(1, policy); err != nil {
		t.Fatal(err)
	}
	if got := assertStatsAccounted(t, c); got.Entries != 1 {
		t.Fatal("Configure removed an active lease", got)
	}
	active.Release(true)
	if got := assertStatsAccounted(t, c); got.Entries != 0 || got.Used != 0 || got.Reserved != 0 {
		t.Fatalf("after last lease release=%+v", got)
	}
}

func TestStatsPartialPruneFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires an unprivileged user")
	}
	c := statsTestCache(t, t.TempDir())
	first := statsTestAcquire(t, c, strings.Repeat("a", 64))
	first.Release(false)
	blocked := statsTestAcquire(t, c, strings.Repeat("b", 64))
	blocked.Release(false)
	active := statsTestAcquire(t, c, strings.Repeat("c", 64))
	defer active.Release(false)
	c.mu.Lock()
	c.entries[first.key].LastUsed = time.Now().Add(-2 * time.Hour)
	c.entries[blocked.key].LastUsed = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	blockedDir := filepath.Join(c.root, blocked.key)
	if err := os.Chmod(blockedDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blockedDir, 0o700) })
	if err := c.Configure(1, c.policy); err == nil {
		t.Fatal("pruning a non-writable cache directory did not fail")
	}
	if got := assertStatsAccounted(t, c); got.Entries != 2 || got.Used <= 0 {
		t.Fatalf("partially pruned stats=%+v", got)
	}
	if _, err := os.Stat(first.Set.Frames[0].Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("earlier cache entry was not pruned", err)
	}
	if _, err := os.Stat(active.Set.Frames[0].Path); err != nil {
		t.Fatal("partial pruning removed active frames", err)
	}
	if err := os.Chmod(blockedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := c.Clear()
	if err != nil || result.Deleted != 1 || result.Retained != 1 {
		t.Fatal(result, err)
	}
	if got := assertStatsAccounted(t, c); got.Entries != 1 {
		t.Fatalf("after retry=%+v", got)
	}
	active.Release(false)
	if _, err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := assertStatsAccounted(t, c); got.Entries != 0 || got.Used != 0 {
		t.Fatalf("after full clear=%+v", got)
	}
}

// Isolate the bookkeeping cost of keeping a large cache. These in-memory
// records are never validated, persisted or removed, and need no media files.
func BenchmarkCacheSweepRetained(b *testing.B) {
	policy := systemsettings.Default().Policy
	policy.SuccessCacheMode, policy.IdleCacheHours = "keep", 0
	c, err := New(b.TempDir(), 1<<60, slog.New(slog.DiscardHandler), policy)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { c.Close() })
	now := time.Now()
	for i := range 11000 {
		key := fmt.Sprintf("%064x", i)
		c.entries[key] = &record{Key: key, Bytes: 65536, LastUsed: now.Add(time.Duration(i) * time.Second)}
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := c.Sweep(now); err != nil {
			b.Fatal(err)
		}
	}
	if len(c.entries) != 11000 {
		b.Fatal("retained records were removed")
	}
}
