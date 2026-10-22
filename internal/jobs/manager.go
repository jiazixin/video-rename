// Package jobs owns persistent batches and the bounded media worker pool.
package jobs

import (
	"context"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/library"
	"video-rename/internal/models"
	"video-rename/internal/pipeline"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

type Manager struct {
	DB           *storage.Store
	Library      *library.Library
	createMu     sync.Mutex
	mu           sync.Mutex
	active       map[int64]activeRun
	liveMu       sync.Mutex
	live         map[int64]LiveStage
	Cache        *framecache.Cache
	extractSlots limiter
	modelSlots   limiter
	Prepare      func(context.Context, *config.TaskConfig, domain.VideoJob, string, string, string) (domain.FrameSet, error)
	Infer        func(context.Context, *config.TaskConfig, []domain.Frame) (string, error)
	Analyze      func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error)
}

type activeRun struct {
	targets []int64
	cancel  context.CancelFunc
	control *runControl
}

func New(db *storage.Store, l *library.Library, cfg *config.Config) (*Manager, error) {
	if err := (models.Store{DB: db}).Seed(); err != nil {
		return nil, err
	}
	if err := interruptRuns(db); err != nil {
		return nil, err
	}
	if err := recoverExtractions(db); err != nil {
		return nil, err
	}
	settings, err := (systemsettings.Store{DB: db}).Get()
	if err != nil {
		return nil, err
	}
	temp := cfg.TempStorage
	if temp == "" {
		var seq int
		var name, path string
		if err = db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
			return nil, err
		}
		temp = filepath.Join(filepath.Dir(path), ".cache")
	}
	cache, err := framecache.New(temp, settings.Bytes(), settings.Policy)
	if err != nil {
		return nil, err
	}
	m := &Manager{DB: db, Library: l, active: map[int64]activeRun{}, Cache: cache, Prepare: pipeline.Prepare, Infer: pipeline.Infer}
	l.Busy = m.Busy
	return m, nil
}

func interruptRuns(db *storage.Store) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err = tx.Exec(`UPDATE run_items SET status='cancelled',message='服务重启，处理已中断',finished_ms=?
WHERE status IN ('queued','running') AND run IN (SELECT id FROM runs WHERE status IN `+domain.ActiveRunStatusesSQL+`)`, now.UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE runs SET status='interrupted',finished=?,error='服务重启，前次运行已中断' WHERE status IN "+domain.ActiveRunStatusesSQL, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) Busy(id int64) bool {
	// Mutations affect the entry's entire subtree. Reuse the persistent scope
	// check so queued runs, paused runs, hard links and nonrecursive targets
	// follow the same occupancy rules as another batch submission. Do not take
	// createMu: callers may already hold Library.mu while a worker is scanning.
	return m.checkOverlap(Request{Targets: []int64{id}, Recursive: true}, newScopeIndex(m.DB)) != nil
}
func (m *Manager) Tools() (string, string, error) {
	p, e := exec.LookPath("ffprobe")
	if e != nil {
		return "", "", &domain.JobError{Code: domain.CodeToolNotFound, Message: "未找到 ffprobe", Global: true}
	}
	f, e := exec.LookPath("ffmpeg")
	if e != nil {
		return "", "", &domain.JobError{Code: domain.CodeToolNotFound, Message: "未找到 ffmpeg", Global: true}
	}
	return p, f, nil
}
func (m *Manager) Cancel(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, running := m.active[id]
	status, finished := "cancelled", storage.Now()
	if running {
		status, finished = "cancelling", ""
	}
	_, err := m.DB.Exec("UPDATE runs SET status=?,finished=? WHERE id=? AND status IN ('queued','scanning','running','paused')", status, finished, id)
	if err == nil {
		if r, ok := m.active[id]; ok {
			r.cancel()
		}
	}
	return err
}
func (m *Manager) Loop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { m.Cache.Loop(ctx) })
	launched := map[int64]bool{}
	finished := make(chan int64)
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-finished:
			delete(launched, id)
		case <-ticker.C:
			limits, err := (systemsettings.Store{DB: m.DB}).BatchLimits()
			if err != nil || len(launched) >= limits.RunningBatchLimit {
				continue
			}
			rows, err := m.DB.Read().QueryContext(ctx, "SELECT id FROM runs WHERE status='queued' ORDER BY id LIMIT ?", limits.RunningBatchLimit)
			if err != nil {
				continue
			}
			var ids []int64
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
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
