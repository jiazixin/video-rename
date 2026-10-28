package job

import (
	"context"
	"database/sql"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/library"
	"video-rename/internal/model"
	"video-rename/internal/pipeline"
	"video-rename/internal/storage"
	"video-rename/internal/systemsetting"
	"video-rename/internal/timefmt"
)

// Manager owns persistent batches and the bounded media worker pool. createMu
// serializes submissions, mu protects active runs, and liveMu protects live stages.
type Manager struct {
	logger       *slog.Logger
	db           *storage.Store
	Library      *library.Library
	createMu     sync.Mutex
	mu           sync.Mutex
	active       map[int64]activeRun
	liveMu       sync.Mutex
	live         map[int64]LiveStage
	Cache        *framecache.Cache
	extractSlots limiter
	modelSlots   limiter
	prepare      func(context.Context, pipeline.PrepareRequest) (domain.FrameSet, error)
	infer        func(context.Context, *config.TaskConfig, []domain.Frame) (string, error)
	analyzeTest  func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error)
	after        func(time.Duration) <-chan time.Time // time.After; replaced in tests
}

type activeRun struct {
	targets []int64
	cancel  context.CancelFunc
	control *runControl
}

// Options groups the inputs for New.
type Options struct {
	Store   *storage.Store
	Library *library.Library
	Config  *config.Config
	Logger  *slog.Logger
}

// New creates the batch manager and seeds the default model settings.
func New(ctx context.Context, params Options) (*Manager, error) {
	db, l, cfg, logger := params.Store, params.Library, params.Config, params.Logger

	if err := (model.New(db)).Seed(ctx); err != nil {
		return nil, err
	}
	if err := interruptRuns(ctx, db); err != nil {
		return nil, err
	}
	if err := recoverExtractions(ctx, db); err != nil {
		return nil, err
	}
	settings, err := (systemsetting.New(db)).Get(ctx)
	if err != nil {
		return nil, err
	}
	temp := cfg.TempStorage
	if temp == "" {
		var seq int
		var name, path string
		if err = db.View(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path)
		}); err != nil {
			return nil, err
		}
		temp = filepath.Join(filepath.Dir(path), ".cache")
	}
	cache, err := framecache.New(ctx, framecache.Options{Base: temp, LimitBytes: settings.Bytes(), Logger: logger, Policy: new(settings.Policy)})
	if err != nil {
		return nil, err
	}
	m := &Manager{logger: logger, db: db, Library: l, active: map[int64]activeRun{}, Cache: cache, prepare: pipeline.Prepare, infer: pipeline.Infer, after: time.After}
	l.Busy = m.Busy
	return m, nil
}

func interruptRuns(ctx context.Context, db *storage.Store) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		now := time.Now()
		if _, err = tx.ExecContext(ctx, `UPDATE run_items SET status='cancelled',message=?,finished_ms=?
WHERE status IN ('queued','running') AND run IN (SELECT id FROM runs WHERE status IN `+domain.ActiveRunStatusesSQL+`)`, recordItemInterrupted, now.UnixMilli()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='interrupted',finished=?,error=? WHERE status IN "+domain.ActiveRunStatusesSQL, timefmt.Format(now), recordRunInterrupted); err != nil {
			return err
		}
		return nil
	})
}

// Busy reports whether entry id lies within the scope of an unfinished batch.
func (m *Manager) Busy(ctx context.Context, id int64) bool {
	// Mutations affect the entry's entire subtree. Reuse the persistent scope
	// check so queued runs, paused runs, hard links and nonrecursive targets
	// follow the same occupancy rules as another batch submission. Do not take
	// createMu: callers may already hold Library.mu while a worker is scanning.
	return m.checkOverlap(ctx, Request{Targets: []int64{id}, Recursive: true}, newScopeIndex(m.db)) != nil
}

// Tools returns the paths of ffprobe and ffmpeg.
func (m *Manager) Tools() (string, string, error) {
	p, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", "", &domain.JobError{Code: domain.CodeToolNotFound, Message: "ffprobe not found", Details: map[string]any{"tool": "ffprobe"}, Global: true}
	}
	f, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", "", &domain.JobError{Code: domain.CodeToolNotFound, Message: "ffmpeg not found", Details: map[string]any{"tool": "ffmpeg"}, Global: true}
	}
	return p, f, nil
}

// Cancel cancels batch id.
func (m *Manager) Cancel(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, running := m.active[id]
	status, finished := "cancelled", storage.Now()
	if running {
		status, finished = "cancelling", ""
	}
	_, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE runs SET status=?,finished=? WHERE id=? AND status IN ('queued','scanning','running','paused')", status, finished, id)
	})
	if err == nil {
		if r, ok := m.active[id]; ok {
			r.cancel()
		}
	}
	return err
}

// Run starts queued batches every second until ctx ends, then waits for
// running batches and the cache sweeper to stop.
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() {
		if err := m.Cache.Run(ctx); err != nil {
			m.logger.Error("frame_cache_worker_failed", "error", err)
		}
	})
	launched := map[int64]bool{}
	finished := make(chan int64)
	for {
		select {
		case <-ctx.Done():
			return nil
		case id := <-finished:
			delete(launched, id)
		case <-ticker.C:
			limits, err := (systemsetting.New(m.db)).BatchLimits(ctx)
			if err != nil {
				m.logger.Error("read_batch_limits_failed", "error", err)
				continue
			}
			if len(launched) >= limits.RunningBatchLimit {
				continue
			}
			ids, err := m.queued(ctx, limits.RunningBatchLimit)
			if err != nil {
				m.logger.Error("read_queued_runs_failed", "error", err)
				continue
			}
			for _, id := range ids {
				if launched[id] || len(launched) >= limits.RunningBatchLimit {
					continue
				}
				launched[id] = true
				wg.Go(func() {
					m.Execute(ctx, id)
					select {
					case finished <- id:
					case <-ctx.Done():
					}
				})
			}
		}
	}
}

func (m *Manager) queued(ctx context.Context, limit int) ([]int64, error) {
	return storage.ReadValue(ctx, m.db, func(tx *sql.Tx) ([]int64, error) {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE status='queued' ORDER BY id LIMIT ?", limit)
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, func(rows *sql.Rows) (int64, error) {
			var id int64
			err := rows.Scan(&id)
			return id, err
		})
	})
}
