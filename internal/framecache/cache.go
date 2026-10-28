// Package framecache manages reusable frame sets with leases and disk reservations.
package framecache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/systemsetting"
	"video-rename/internal/timefmt"
)

type record struct {
	Key         string
	Set         domain.FrameSet
	LastUsed    timefmt.Time
	Bytes       int64
	pins        int
	SucceededAt timefmt.Time
	Checksums   map[string]string
}

// Cache stores reusable frame sets on disk within a byte limit. mu protects
// entries, building, reservations and policy; stats is an atomic snapshot.
type Cache struct {
	logger    *slog.Logger
	now       func() time.Time
	mu        sync.Mutex
	lock      *os.File
	root      string
	entries   map[string]*record
	building  map[string]bool
	usedBytes int64
	reserved  int64
	limit     int64
	policy    systemsetting.Policy
	changed   chan struct{}
	stats     atomic.Pointer[Stats]
}

// Lease keeps a cached frame set from being evicted while it is in use.
type Lease struct {
	cache *Cache
	key   string
	Set   domain.FrameSet
	once  sync.Once
}

// Stats describes the cache location and usage.
type Stats struct {
	Path     string `json:"path"`
	Used     int64  `json:"used_bytes"`
	Reserved int64  `json:"reserved_bytes"`
	Entries  int    `json:"entries"`
}

// Key returns the cache key of the frames of job extracted with cfg.
func Key(job domain.VideoJob, cfg *config.TaskConfig) string {
	// The key contains only validated finite settings and fixed scalar types.
	raw, _ := json.Marshal(struct {
		Version  int `json:"version"`
		Snapshot struct {
			Dev        uint64 `json:"device"`
			Ino        uint64 `json:"inode"`
			Size       int64  `json:"size"`
			ModifiedNS int64  `json:"modified_ns"`
		} `json:"snapshot"`
		Sampling config.SamplingConfig `json:"sampling"`
		Images   config.ImagesConfig   `json:"images"`
	}{3, struct {
		Dev        uint64 `json:"device"`
		Ino        uint64 `json:"inode"`
		Size       int64  `json:"size"`
		ModifiedNS int64  `json:"modified_ns"`
	}{job.Snapshot.Dev, job.Snapshot.Ino, job.Snapshot.Size, job.Snapshot.ModTime.UnixNano()}, cfg.Sampling, cfg.Images})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// Options configures the cache root, capacity and retention policy.
type Options struct {
	Base       string
	LimitBytes int64
	Logger     *slog.Logger
	Policy     *systemsetting.Policy
}

// New opens the frame cache under base with a byte limit.
func New(ctx context.Context, options Options) (_ *Cache, err error) {
	base, limit, logger := options.Base, options.LimitBytes, options.Logger
	if logger == nil {
		return nil, errors.New("frame cache logger is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := config.FrameCachePath(base)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("frame cache directory is invalid")
	}
	lock, err := os.OpenFile(filepath.Join(root, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("lock frame cache directory: %w", err), lock.Close())
	}
	ok := false
	defer func() {
		if !ok {
			err = errors.Join(err, lock.Close())
		}
	}()
	c := &Cache{logger: logger, now: time.Now, lock: lock, root: root, limit: limit, entries: map[string]*record{}, building: map[string]bool{}, changed: make(chan struct{})}
	c.policy = systemsetting.Default().Policy
	if options.Policy != nil {
		c.policy = *options.Policy
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Only generated namespaces are managed. Never traverse unknown paths or symlinks.
		if !d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			continue
		}
		if strings.HasPrefix(d.Name(), "pending-") {
			if err = os.RemoveAll(filepath.Join(root, d.Name())); err != nil {
				return nil, err
			}
			continue
		}
		if !validKey(d.Name()) {
			continue
		}
		var r record
		raw, e := readCacheFile(ctx, filepath.Join(root, d.Name()), "manifest.json")
		if e == nil {
			r, e = unmarshalRecord(raw)
		}
		if e == nil && r.Key == d.Name() {
			e = c.validate(ctx, &r)
		} else {
			e = errors.New("invalid manifest")
		}
		if e != nil {
			if err = os.RemoveAll(filepath.Join(root, d.Name())); err != nil {
				return nil, err
			}
			continue
		}
		c.entries[r.Key] = &r
		c.usedBytes += r.Bytes
	}
	if err = c.pruneLocked(ctx, c.now(), 0); err != nil {
		return nil, err
	}
	c.publishStatsLocked()
	ok = true
	return c, nil
}

// Close releases the cache directory lock.
func (c *Cache) Close() error { return c.lock.Close() }

func validKey(key string) bool {
	b, err := hex.DecodeString(key)
	return err == nil && len(b) == 32 && strings.ToLower(key) == key
}
func (c *Cache) signal() {
	c.publishStatsLocked()
	close(c.changed)
	c.changed = make(chan struct{})
}

// publishStatsLocked records completed accounting changes while mu is held,
// or during construction before the cache is exposed to other goroutines.
func (c *Cache) publishStatsLocked() {
	c.stats.Store(&Stats{Path: c.root, Used: c.usedBytes, Reserved: c.reserved, Entries: len(c.entries)})
}

// Stats returns the latest accounting snapshot without waiting for checksum,
// manifest, or cleanup I/O performed under the cache's work lock.
func (c *Cache) Stats() Stats {
	return *c.stats.Load()
}

// Sweep evicts expired and excess cache entries.
func (c *Cache) Sweep(ctx context.Context, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pruneLocked(ctx, now, 0)
}

// Run sweeps the cache every minute until ctx ends.
func (c *Cache) Run(ctx context.Context) error {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if err := c.Sweep(ctx, now); err != nil {
				c.logger.Error("frame_cache_clean_failed", "error", err)
			}
		}
	}
}
func (c *Cache) remove(r *record) error {
	if err := os.RemoveAll(filepath.Join(c.root, r.Key)); err != nil {
		return err
	}
	delete(c.entries, r.Key)
	c.usedBytes -= r.Bytes
	c.signal()
	return nil
}
func (c *Cache) pruneLocked(ctx context.Context, now time.Time, need int64) error {
	all := make([]*record, 0, len(c.entries))
	for r := range maps.Values(c.entries) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.pins == 0 {
			all = append(all, r)
		}
	}
	slices.SortFunc(all, func(a, b *record) int { return a.LastUsed.Compare(b.LastUsed) })
	for _, r := range all {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.expired(r, now) || c.usedBytes+c.reserved+need > c.limit {
			if err := c.remove(r); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *Cache) persist(r *record) error {
	dir := filepath.Join(c.root, r.Key)
	raw, err := marshalRecord(r)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.next"), raw, 0o600); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, "manifest.next"), filepath.Join(dir, "manifest.json"))
}
func (c *Cache) validate(ctx context.Context, r *record) error {
	dir := filepath.Join(c.root, r.Key)
	if len(r.Set.Frames) == 0 || len(r.Set.Frames) > 32 {
		return errors.New("cached frames are incomplete")
	}
	// Stored paths are relative, preventing manifests from redirecting model reads elsewhere.
	for i := range r.Set.Frames {
		if err := ctx.Err(); err != nil {
			return err
		}
		f := &r.Set.Frames[i]
		if filepath.Base(f.Path) != f.Path || f.Path == "." {
			return errors.New("cache path is invalid")
		}
		st, err := os.Lstat(filepath.Join(dir, f.Path))
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() || st.Size() != int64(f.Bytes) || f.Bytes <= 0 || f.Bytes > 1<<20 {
			return errors.New("cached frames changed")
		}
		data, err := readCacheFile(ctx, dir, f.Path)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		if r.Checksums[f.Path] != hex.EncodeToString(hash[:]) {
			return errors.New("cached frame check failed")
		}
	}
	var size int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if p != dir {
				return errors.New("cache contains an unknown directory")
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("cache contains a non-regular file")
		}
		st, e := d.Info()
		if e == nil {
			size += st.Size()
		}
		return e
	})
	r.Bytes = size + 65536 // reserve manifest growth as well
	return err
}
func (c *Cache) lease(r *record) *Lease {
	r.pins++
	set := r.Set
	set.Frames = slices.Clone(r.Set.Frames)
	set.TempDir = filepath.Join(c.root, r.Key)
	for i := range set.Frames {
		set.Frames[i].Path = filepath.Join(set.TempDir, set.Frames[i].Path)
	}
	return &Lease{cache: c, key: r.Key, Set: set}
}

// Acquire reserves peak workspace before extraction; full caches wait without spinning.
// build writes only under work and returns a complete, validated frame set.
func (c *Cache) Acquire(ctx context.Context, key string, reserve int64, build func(string) (domain.FrameSet, error)) (*Lease, error) {
	if !validKey(key) {
		return nil, errors.New("cache key is invalid")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if r := c.entries[key]; r != nil {
			previousBytes := r.Bytes
			err := c.validate(ctx, r)
			c.usedBytes += r.Bytes - previousBytes
			c.publishStatsLocked()
			if err == nil {
				r.LastUsed = timefmt.Time(c.now())
				if err = c.persist(r); err != nil {
					c.mu.Unlock()
					return nil, err
				}
				l := c.lease(r)
				c.mu.Unlock()
				return l, nil
			} else if r.pins == 0 {
				if err = c.remove(r); err != nil {
					c.mu.Unlock()
					return nil, err
				}
			} else {
				c.mu.Unlock()
				return nil, err
			}
		}
		if reserve > c.limit {
			c.mu.Unlock()
			return nil, domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonTempCapacityTooSmall, Message: "temp directory capacity cannot hold one video"})
		}
		if err := c.pruneLocked(ctx, c.now(), reserve); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		if !c.building[key] && c.usedBytes+c.reserved+reserve <= c.limit {
			c.building[key] = true
			c.reserved += reserve
			c.publishStatsLocked()
			c.mu.Unlock()
			break
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
	defer func() { c.mu.Lock(); delete(c.building, key); c.reserved -= reserve; c.signal(); c.mu.Unlock() }()
	work, err := os.MkdirTemp(c.root, "pending-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	set, err := build(work)
	if err != nil {
		return nil, err
	}
	// ctx controls admission and may be cancelled by pausing the scheduler.
	// The builder owns the running job's cancellation. Once it succeeds, finish
	// publishing its frames within a bounded cleanup window instead of losing
	// completed work when admission closes.
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	// Preserve only frames selected by the extractor, discarding failed/recompression intermediates.
	dir := filepath.Join(work, "ready")
	if err = os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	for i := range set.Frames {
		f := &set.Frames[i]
		rel, e := filepath.Rel(work, f.Path)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return nil, errors.New("extraction output escapes its directory")
		}
		st, e := os.Lstat(f.Path)
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() || st.Size() != int64(f.Bytes) {
			return nil, errors.New("extraction output is invalid")
		}
		name := fmt.Sprintf("frame-%02d.jpg", i)
		if err = os.Rename(f.Path, filepath.Join(dir, name)); err != nil {
			return nil, err
		}
		f.Path = name
	}
	set.TempDir = ""
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = os.Rename(dir, filepath.Join(c.root, key)); err != nil {
		return nil, err
	}
	r := &record{Key: key, Set: set, LastUsed: timefmt.Time(c.now()), Checksums: map[string]string{}}
	for _, f := range set.Frames {
		data, e := readCacheFile(publishCtx, filepath.Join(c.root, key), f.Path)
		if e != nil {
			return nil, errors.Join(e, os.RemoveAll(filepath.Join(c.root, key)))
		}
		hash := sha256.Sum256(data)
		r.Checksums[f.Path] = hex.EncodeToString(hash[:])
	}
	if err = c.validate(publishCtx, r); err == nil && r.Bytes > reserve {
		err = domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonFramesOverReservation, Message: "frames exceed the reserved capacity"})
	}
	if err == nil {
		err = c.persist(r)
	}
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(filepath.Join(c.root, key)))
	}
	c.entries[key] = r
	c.usedBytes += r.Bytes
	c.publishStatsLocked()
	return c.lease(r), nil
}

// Release records successful commits and applies the current retention policy.
func (l *Lease) Release(success bool) {
	l.once.Do(func() {
		c := l.cache
		c.mu.Lock()
		defer c.mu.Unlock()
		r := c.entries[l.key]
		if r == nil {
			return
		}
		r.pins--
		r.LastUsed = timefmt.Time(c.now())
		var err error
		if success {
			r.SucceededAt = timefmt.Time(c.now())
		}
		if c.expired(r, c.now()) && r.pins == 0 {
			err = c.remove(r)
		} else {
			err = c.persist(r)
		}
		if err != nil {
			c.logger.Error("frame_cache_update_failed", "error", err)
		}
		c.signal()
	})
}

// Configure applies current retention settings without touching active leases.
func (c *Cache) Configure(ctx context.Context, limit int64, policy systemsetting.Policy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limit, c.policy = limit, policy
	c.signal()
	return c.pruneLocked(ctx, c.now(), 0)
}
func (c *Cache) expired(r *record, now time.Time) bool {
	if c.policy.IdleCacheHours > 0 && now.Sub(time.Time(r.LastUsed)) >= time.Duration(c.policy.IdleCacheHours)*time.Hour {
		return true
	}
	if r.SucceededAt.IsZero() {
		return false
	}
	switch c.policy.SuccessCacheMode {
	case "", "immediate":
		return true
	case "delayed":
		return now.Sub(time.Time(r.SucceededAt)) >= time.Duration(c.policy.SuccessCacheHours)*time.Hour
	default:
		return false
	}
}

// ClearResult reports what clearing the cache removed.
type ClearResult struct {
	Deleted    int   `json:"deleted"`
	FreedBytes int64 `json:"freed_bytes"`
	Retained   int   `json:"retained"`
}

// Clear removes only completed, idle entries. Active builders and leases survive.
func (c *Cache) Clear(ctx context.Context) (ClearResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := ClearResult{Retained: len(c.building)}
	for r := range maps.Values(c.entries) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if r.pins > 0 {
			if !c.building[r.Key] {
				result.Retained++
			}
			continue
		}
		size := r.Bytes
		if err := c.remove(r); err != nil {
			return result, err
		}
		result.Deleted++
		result.FreedBytes += size
	}
	return result, nil
}
