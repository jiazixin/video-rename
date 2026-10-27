package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"sync"
)

const reportCacheLimit = 8 << 20

var errReportTooLarge = errors.New("report exceeds cache capacity")

// Stop serialization at the capacity boundary as well as bounding retained data.
// Large all-history reports must not allocate an unlimited extra JSON copy.
type reportBuffer struct{ buffer bytes.Buffer }

func (b *reportBuffer) Write(p []byte) (int, error) {
	if len(p) > reportCacheLimit-b.buffer.Len() {
		return 0, errReportTooLarge
	}
	return b.buffer.Write(p)
}

type reportCacheEntry struct {
	revision int64
	used     uint64
	data     []byte
}
type reportCache struct {
	mu      sync.Mutex
	entries map[string]reportCacheEntry
	bytes   int
	tick    uint64
}

// ReportRevision is read in the report's transaction, not on another connection.
// Changes and revision increments commit together, including changes by SQL tools.
func ReportRevision(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, "SELECT revision FROM report_revision WHERE name=?", name).Scan(&revision)
	return revision, err
}

// CachedReport decodes a private copy so callers cannot mutate cached results.
func (s *Store) CachedReport(key string, revision int64, target any) bool {
	c := &s.reports
	c.mu.Lock()
	e, ok := c.entries[key]
	if ok && e.revision == revision {
		c.tick++
		e.used = c.tick
		c.entries[key] = e
	} else {
		ok = false
	}
	c.mu.Unlock()
	return ok && json.Unmarshal(e.data, target) == nil
}

// CacheReport stores report under key for data revision revision.
func (s *Store) CacheReport(key string, revision int64, report any) {
	var buffer reportBuffer
	if err := json.MarshalWrite(&buffer, report); err != nil {
		return
	}
	data := buffer.buffer.Bytes()
	c := &s.reports
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]reportCacheEntry{}
	}
	if old, ok := c.entries[key]; ok {
		if old.revision > revision {
			return
		}
		c.bytes -= len(old.data)
		delete(c.entries, key)
	}
	for len(c.entries) >= 32 || c.bytes+len(data) > reportCacheLimit {
		var oldest string
		used := ^uint64(0)
		for k, e := range c.entries {
			if e.used < used {
				oldest, used = k, e.used
			}
		}
		c.bytes -= len(c.entries[oldest].data)
		delete(c.entries, oldest)
	}
	c.tick++
	c.entries[key] = reportCacheEntry{revision: revision, used: c.tick, data: data}
	c.bytes += len(data)
}
