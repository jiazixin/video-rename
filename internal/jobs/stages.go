package jobs

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/models"
)

type limitState struct {
	active, limit int
	nextCheck     time.Time
}
type limiter struct {
	mu      sync.Mutex
	states  map[int64]*limitState
	changed chan struct{}
}

// Waiting tasks share one settings refresh per key, avoiding a DB poll per video.
func (l *limiter) acquire(ctx context.Context, key int64, fallback int, read func() (int, error)) (func(), int, error) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		l.mu.Lock()
		if l.states == nil {
			l.states = map[int64]*limitState{}
			l.changed = make(chan struct{})
		}
		state := l.states[key]
		if state == nil {
			state = &limitState{}
			l.states[key] = state
		}
		if state.limit == 0 || state.active < state.limit || !time.Now().Before(state.nextCheck) {
			n, err := read()
			if errors.Is(err, sql.ErrNoRows) {
				n = state.limit
				if n == 0 {
					n = fallback
				}
				err = nil
			}
			if err != nil {
				l.mu.Unlock()
				return nil, 0, err
			}
			state.limit = n
			state.nextCheck = time.Now().Add(500 * time.Millisecond)
		}
		n := state.limit
		if state.active < n {
			state.active++
			l.mu.Unlock()
			return func() { l.mu.Lock(); state.active--; close(l.changed); l.changed = make(chan struct{}); l.mu.Unlock() }, n, nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-changed:
		case <-tick.C:
		}
	}
}
func (m *Manager) modelSlot(ctx context.Context, id int64, fallback int) (func(), int, error) {
	return m.modelSlots.acquire(ctx, id, fallback, func() (int, error) { return (models.Store{DB: m.DB}).Workers(ctx, id) })
}
func (m *Manager) analyze(ctx context.Context, run Run, entry int64, cfg *config.Config, job domain.VideoJob, probe, ffmpeg string, extracted func()) (string, *framecache.Lease, error) {
	var lease *framecache.Lease
	if m.Analyze == nil {
		var err error
		lease, err = m.prepareFrames(ctx, run, entry, cfg, job, probe, ffmpeg)
		if err != nil {
			return "", nil, err
		}
	}
	extracted()
	title, err := m.inferWithRetry(ctx, run, entry, cfg, job, probe, ffmpeg, lease)
	return title, lease, err
}
