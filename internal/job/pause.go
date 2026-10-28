package job

import (
	"context"
	"database/sql"
	"sync"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/storage"
	"video-rename/internal/systemsetting"
)

// Protected by Manager.mu. Pausing cancels only scheduling waits; work already
// admitted to a stage keeps the run's context and can finish normally.
// waits holds the cancel of every schedule context handed out since the last
// pause, keyed by a token that its holder releases.
type runControl struct {
	phase   string
	paused  bool
	waits   map[*byte]context.CancelFunc
	changed chan struct{}
}

func newRunControl() *runControl {
	return &runControl{phase: "scanning", waits: map[*byte]context.CancelFunc{}, changed: make(chan struct{})}
}

// Pause pauses batch id.
func (m *Manager) Pause(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	result, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE runs SET status='paused' WHERE id=? AND status IN ('queued','scanning','running','paused')", id)
	})
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.Fail(domain.KindConflict, domain.Record{Code: "run_not_pausable", Message: "run finished or is cancelling; cannot pause"})
	}
	if active, ok := m.active[id]; ok && !active.control.paused {
		active.control.paused = true
		for _, cancel := range active.control.waits {
			cancel()
		}
		clear(active.control.waits)
	}
	return nil
}

// Resume resumes paused batch id.
func (m *Manager) Resume(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	phase := "queued"
	active, ok := m.active[id]
	if ok {
		phase = active.control.phase
	}
	result, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE runs SET status=? WHERE id=? AND status='paused'", phase, id)
	})
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.Fail(domain.KindConflict, domain.Record{Code: "run_not_paused", Message: "run is not paused; cannot resume"})
	}
	if ok {
		c := active.control
		c.paused = false
		close(c.changed)
		c.changed = make(chan struct{})
	}
	return nil
}

func (m *Manager) setPhase(ctx context.Context, id int64, phase string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.active[id]; ok {
		active.control.phase = phase
	}
	_, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE runs SET status=? WHERE id=? AND status NOT IN ('paused','cancelling')", phase, id)
	})
	return err
}

// waitDispatch blocks while run id is paused.
func (m *Manager) waitDispatch(ctx context.Context, id int64) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		active, ok := m.active[id]
		if !ok || active.control == nil || !active.control.paused {
			m.mu.Unlock()
			return nil
		}
		changed := active.control.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (m *Manager) extractionSlot(ctx context.Context, run int64) (func(), error) {
	for {
		if err := m.waitDispatch(ctx, run); err != nil {
			return nil, err
		}
		schedule, done := m.scheduleContext(ctx, run)
		release, _, err := m.extractSlots.acquire(schedule, 0, 8, func() (int, error) {
			settings, err := (systemsetting.New(m.db)).Get(ctx)
			return settings.ExtractWorkers, err
		})
		paused := schedule.Err() != nil
		done()
		if paused && ctx.Err() == nil {
			if release != nil {
				release()
			}
			continue
		}
		return release, err
	}
}

// scheduleContext returns a context derived from ctx that also ends when run
// id is paused; it is already done while the run is paused. Call done once the
// scheduling wait is over.
func (m *Manager) scheduleContext(ctx context.Context, id int64) (schedule context.Context, done func()) {
	schedule, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	active, ok := m.active[id]
	if !ok || active.control == nil {
		return schedule, cancel
	}
	if active.control.paused {
		cancel()
		return schedule, cancel
	}
	token := new(byte)
	active.control.waits[token] = cancel
	return schedule, func() {
		m.mu.Lock()
		delete(active.control.waits, token)
		m.mu.Unlock()
		cancel()
	}
}

// inferenceSlotOptions groups the inputs for inferenceSlot.
type inferenceSlotOptions struct {
	Run    Run
	Entry  int64
	Model  int64
	Config *config.TaskConfig
	Job    domain.VideoJob
	Probe  string
	Ffmpeg string
	Lease  **framecache.Lease
}

// Release queued screenshots on pause so another batch can use cache capacity.
// Resume reacquires the same cache key, rebuilding only if idle cleanup evicted it.
func (m *Manager) inferenceSlot(ctx context.Context, params inferenceSlotOptions) (func(), int, error) {
	run, entry, model, cfg, job, probe, ffmpeg, lease := params.Run, params.Entry, params.Model, params.Config, params.Job, params.Probe, params.Ffmpeg, params.Lease

	for {
		schedule, done := m.scheduleContext(ctx, run.ID)
		if schedule.Err() != nil {
			done()
			if *lease != nil {
				(*lease).Release(false)
				*lease = nil
			}
			if err := m.waitDispatch(ctx, run.ID); err != nil {
				return nil, 0, err
			}
			continue
		}
		if *lease == nil && m.analyzeTest == nil {
			done()
			release, err := m.extractionSlot(ctx, run.ID)
			if err != nil {
				return nil, 0, err
			}
			extracted := sync.OnceFunc(release)
			*lease, err = m.prepareFrames(ctx, prepareFramesOptions{Run: run, Entry: entry, Config: cfg, Job: job, Probe: probe, Ffmpeg: ffmpeg, Extracted: extracted})
			extracted()
			if err != nil {
				return nil, 0, err
			}
			continue
		}
		release, n, err := m.modelSlot(schedule, model, cfg.Workers)
		paused := schedule.Err() != nil
		done()
		if paused && ctx.Err() == nil {
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
