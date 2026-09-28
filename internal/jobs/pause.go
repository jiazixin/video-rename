package jobs

import (
	"context"
	"fmt"
	"sync"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/systemsettings"
)

// Protected by Manager.mu. Pausing cancels only scheduling waits; work already
// admitted to a stage keeps the run's parent context and can finish normally.
type runControl struct {
	phase    string
	parent   context.Context
	schedule context.Context
	stop     context.CancelFunc
	paused   bool
	changed  chan struct{}
}

func (m *Manager) Pause(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	result, err := m.DB.Exec("UPDATE runs SET status='paused' WHERE id=? AND status IN ('queued','scanning','running','paused')", id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("该批次已结束或正在取消，无法暂停")
	}
	if active, ok := m.active[id]; ok && !active.control.paused {
		active.control.paused = true
		active.control.stop()
	}
	return nil
}

func (m *Manager) Resume(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	phase := "queued"
	active, ok := m.active[id]
	if ok {
		phase = active.control.phase
	}
	result, err := m.DB.Exec("UPDATE runs SET status=? WHERE id=? AND status='paused'", phase, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("该批次未暂停，无法继续运行")
	}
	if ok {
		c := active.control
		c.schedule, c.stop = context.WithCancel(c.parent)
		c.paused = false
		close(c.changed)
		c.changed = make(chan struct{})
	}
	return nil
}

func (m *Manager) setPhase(id int64, phase string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.active[id]; ok {
		active.control.phase = phase
	}
	_, err := m.DB.Exec("UPDATE runs SET status=? WHERE id=? AND status NOT IN ('paused','cancelling')", phase, id)
	return err
}

func (m *Manager) dispatchContext(ctx context.Context, id int64) (context.Context, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		active, ok := m.active[id]
		if !ok || active.control == nil {
			m.mu.Unlock()
			return ctx, nil
		}
		c := active.control
		if !c.paused {
			schedule := c.schedule
			m.mu.Unlock()
			return schedule, nil
		}
		changed := c.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (m *Manager) extractionSlot(ctx context.Context, run int64) (func(), error) {
	for {
		schedule, err := m.dispatchContext(ctx, run)
		if err != nil {
			return nil, err
		}
		release, _, err := m.extractSlots.acquire(schedule, 0, 8, func() (int, error) {
			settings, err := (systemsettings.Store{DB: m.DB}).Get()
			return settings.ExtractWorkers, err
		})
		if schedule.Err() != nil && ctx.Err() == nil {
			if release != nil {
				release()
			}
			continue
		}
		return release, err
	}
}

func (m *Manager) currentSchedule(ctx context.Context, id int64) context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.active[id]; ok && active.control != nil {
		return active.control.schedule
	}
	return ctx
}

// Release queued screenshots on pause so another batch can use cache capacity.
// Resume reacquires the same cache key, rebuilding only if idle cleanup evicted it.
func (m *Manager) inferenceSlot(ctx context.Context, run Run, entry, model int64, cfg *config.Config, job domain.VideoJob, probe, ffmpeg string, lease **framecache.Lease) (func(), int, error) {
	for {
		schedule := m.currentSchedule(ctx, run.ID)
		if schedule.Err() != nil {
			if *lease != nil {
				(*lease).Release(false)
				*lease = nil
			}
			if _, err := m.dispatchContext(ctx, run.ID); err != nil {
				return nil, 0, err
			}
			continue
		}
		if *lease == nil && m.Analyze == nil {
			release, err := m.extractionSlot(ctx, run.ID)
			if err != nil {
				return nil, 0, err
			}
			extracted := sync.OnceFunc(release)
			*lease, err = m.prepareFrames(ctx, run, entry, cfg, job, probe, ffmpeg, extracted)
			extracted()
			if err != nil {
				return nil, 0, err
			}
			continue
		}
		release, n, err := m.modelSlot(schedule, model, cfg.Workers)
		if schedule.Err() != nil && ctx.Err() == nil {
			if release != nil {
				release()
			}
			if *lease != nil {
				(*lease).Release(false)
				*lease = nil
			}
			continue
		}
		return release, n, err
	}
}
