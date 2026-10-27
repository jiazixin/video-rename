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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/systemsettings"
)

type record struct {
	Key         string
	Set         domain.FrameSet
	LastUsed    time.Time
	Bytes       int64
	pins        int
	SucceededAt time.Time
	Checksums   map[string]string
}

// Cache stores reusable frame sets on disk within a byte limit.
type Cache struct {
	logger    *slog.Logger
	mu        sync.Mutex
	lock      *os.File
	root      string
	entries   map[string]*record
	building  map[string]bool
	usedBytes int64
	reserved  int64
	limit     int64
	policy    systemsettings.Policy
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

// durationOptions round-trips time.Duration as integer nanoseconds, the format
// existing manifests and cache keys use; FrameSet has several Duration fields,
// which json/v2 does not serialize by default.
var durationOptions = json.JoinOptions(
	json.WithMarshalers(json.MarshalFunc(func(d time.Duration) ([]byte, error) {
		return strconv.AppendInt(nil, int64(d), 10), nil
	})),
	json.WithUnmarshalers(json.UnmarshalFunc(func(raw []byte, d *time.Duration) error {
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return fmt.Errorf("parse duration %s: %w", raw, err)
		}
		*d = time.Duration(n)
		return nil
	})),
)

// Key returns the cache key of the frames of job extracted with cfg.
func Key(job domain.VideoJob, cfg *config.TaskConfig) string {
	raw, _ := json.Marshal(struct {
		Version  int
		Snapshot domain.Snapshot
		Sampling config.SamplingConfig
		Images   config.ImagesConfig
	}{1, job.Snapshot, cfg.Sampling, cfg.Images}, durationOptions)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// New opens the frame cache under base with a byte limit.
func New(base string, limit int64, logger *slog.Logger, policies ...systemsettings.Policy) (*Cache, error) {
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
		lock.Close()
		return nil, errors.New("frame cache directory is in use by another instance")
	}
	ok := false
	defer func() {
		if !ok {
			lock.Close()
		}
	}()
	c := &Cache{lock: lock, root: root, limit: limit, entries: map[string]*record{}, building: map[string]bool{}, changed: make(chan struct{})}
	c.policy = systemsettings.Default().Policy
	if len(policies) > 0 {
		c.policy = policies[0]
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
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
		raw, e := os.ReadFile(filepath.Join(root, d.Name(), "manifest.json"))
		if e == nil {
			e = json.Unmarshal(raw, &r, durationOptions)
		}
		if e == nil && r.Key == d.Name() {
			e = c.validate(&r)
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
	if err = c.pruneLocked(time.Now(), 0); err != nil {
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
func (c *Cache) Sweep(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pruneLocked(now, 0)
}

// Loop sweeps the cache every minute until ctx ends.
func (c *Cache) Loop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := c.Sweep(now); err != nil {
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
func (c *Cache) pruneLocked(now time.Time, need int64) error {
	all := make([]*record, 0, len(c.entries))
	for r := range maps.Values(c.entries) {
		if r.pins == 0 {
			all = append(all, r)
		}
	}
	slices.SortFunc(all, func(a, b *record) int { return a.LastUsed.Compare(b.LastUsed) })
	for _, r := range all {
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
	raw, err := json.Marshal(r, durationOptions)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.next"), raw, 0o600); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, "manifest.next"), filepath.Join(dir, "manifest.json"))
}
func (c *Cache) validate(r *record) error {
	dir := filepath.Join(c.root, r.Key)
	if len(r.Set.Frames) == 0 || len(r.Set.Frames) > 32 {
		return errors.New("cached frames are incomplete")
	}
	// Stored paths are relative, preventing manifests from redirecting model reads elsewhere.
	for i := range r.Set.Frames {
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
		data, err := os.ReadFile(filepath.Join(dir, f.Path))
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
			err := c.validate(r)
			c.usedBytes += r.Bytes - previousBytes
			c.publishStatsLocked()
			if err == nil {
				r.LastUsed = time.Now()
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
			return nil, domain.Fail(domain.KindUnavailable, "temp_capacity_too_small", "temp directory capacity cannot hold one video")
		}
		if err := c.pruneLocked(time.Now(), reserve); err != nil {
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
	r := &record{Key: key, Set: set, LastUsed: time.Now(), Checksums: map[string]string{}}
	for _, f := range set.Frames {
		data, e := os.ReadFile(filepath.Join(c.root, key, f.Path))
		if e != nil {
			_ = os.RemoveAll(filepath.Join(c.root, key))
			return nil, e
		}
		hash := sha256.Sum256(data)
		r.Checksums[f.Path] = hex.EncodeToString(hash[:])
	}
	if err = c.validate(r); err == nil && r.Bytes > reserve {
		err = domain.Fail(domain.KindUnavailable, "frames_over_reservation", "frames exceed the reserved capacity")
	}
	if err == nil {
		err = c.persist(r)
	}
	if err != nil {
		_ = os.RemoveAll(filepath.Join(c.root, key))
		return nil, err
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
		r.LastUsed = time.Now()
		var err error
		if success {
			r.SucceededAt = time.Now()
		}
		if c.expired(r, time.Now()) && r.pins == 0 {
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
func (c *Cache) Configure(limit int64, policy systemsettings.Policy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limit, c.policy = limit, policy
	c.signal()
	return c.pruneLocked(time.Now(), 0)
}
func (c *Cache) expired(r *record, now time.Time) bool {
	if c.policy.IdleCacheHours > 0 && now.Sub(r.LastUsed) >= time.Duration(c.policy.IdleCacheHours)*time.Hour {
		return true
	}
	if r.SucceededAt.IsZero() {
		return false
	}
	switch c.policy.SuccessCacheMode {
	case "", "immediate":
		return true
	case "delayed":
		return now.Sub(r.SucceededAt) >= time.Duration(c.policy.SuccessCacheHours)*time.Hour
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
func (c *Cache) Clear() (ClearResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := ClearResult{Retained: len(c.building)}
	for r := range maps.Values(c.entries) {
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
