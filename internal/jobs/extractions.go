package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/storage"
)

func recoverExtractions(db *storage.Store) error {
	now := storage.Now()
	_, err := db.Exec(`UPDATE extraction_records SET status='failure',finished=?,duration_ms=MAX(0,CAST((julianday(?)-julianday(started))*86400000 AS INTEGER)),message='服务重启，抽帧已中断' WHERE status='running'`, now, now)
	return err
}

// The record ends when usable frames are ready, independently of inference and rename.
// Cache hits are explicit so their short duration is not mistaken for a fresh extraction.
func (m *Manager) prepareFrames(ctx context.Context, run Run, entry int64, cfg *config.Config, job domain.VideoJob, probe, ffmpeg string, extracted func()) (*framecache.Lease, error) {
	// A pause interrupts capacity waits, but never an extraction already started.
	release := extracted
	defer func() { release() }()
	for {
		schedule := m.currentSchedule(ctx, run.ID)
		lease, err := m.prepareFramesOnce(ctx, schedule, run, entry, cfg, job, probe, ffmpeg)
		if err == nil || !errors.Is(err, context.Canceled) || schedule.Err() == nil || ctx.Err() != nil {
			return lease, err
		}
		release()
		release, err = m.extractionSlot(ctx, run.ID)
		if err != nil {
			release = func() {}
			return nil, err
		}
	}
}

func (m *Manager) prepareFramesOnce(ctx, schedule context.Context, run Run, entry int64, cfg *config.Config, job domain.VideoJob, probe, ffmpeg string) (lease *framecache.Lease, err error) {
	started := time.Now()
	var id int64
	cached := true
	start := func() error {
		var err error
		id, err = m.startExtraction(run, entry, m.Library.DisplayPath(entry), started)
		return err
	}
	defer func() {
		if id == 0 {
			return
		}
		frames := 0
		if lease != nil {
			frames = len(lease.Set.Frames)
		}
		m.finishExtraction(id, started, cached && err == nil, frames, err)
	}()
	reserve := int64(cfg.Sampling.MaxFrames)*(1<<20) + (32 << 20) + 65536
	lease, err = m.Cache.Acquire(schedule, framecache.Key(job, cfg), reserve, func(work string) (domain.FrameSet, error) {
		// The slot was admitted before pause; only cancellation stops its work.
		cached = false
		if err := start(); err != nil {
			return domain.FrameSet{}, fmt.Errorf("无法记录抽帧开始: %w", err)
		}
		return m.Prepare(ctx, cfg, job, probe, ffmpeg, work)
	})
	if id == 0 && (err == nil || schedule.Err() == nil || ctx.Err() != nil) {
		if recordErr := start(); recordErr != nil {
			if lease != nil {
				lease.Release(false)
				lease = nil
			}
			return nil, fmt.Errorf("无法记录抽帧开始: %w", recordErr)
		}
	}
	if err == nil {
		var current domain.Snapshot
		current, err = domain.StatSnapshot(job.Path)
		if err == nil && !current.Same(job.Snapshot) {
			err = domain.SourceChangedError("视频在抽帧期间发生变化，请重新抽帧", "抽帧完成后核验原视频", job.Path, job.Snapshot, current)
		}
		if err != nil {
			lease.Release(true)
			lease = nil
		}
	}
	return
}

func (m *Manager) startExtraction(run Run, entry int64, path string, started time.Time) (int64, error) {
	source := "automatic"
	if run.Task == "extract" {
		source = "manual"
	}
	res, err := m.DB.Exec(`INSERT INTO extraction_records(run,entry,path,source,status,started) VALUES(?,?,?,?,'running',?)`, run.ID, entry, path, source, started.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (m *Manager) finishExtraction(id int64, started time.Time, cached bool, frames int, extractionErr error) {
	finished := time.Now()
	status, message, detail := "success", "", ""
	if extractionErr != nil {
		status, message = "failure", extractionErr.Error()
		if errors.Is(extractionErr, context.Canceled) {
			message = "抽帧已取消"
		}
		if diagnostic, ok := errors.AsType[*domain.JobError](extractionErr); ok {
			detail = strings.TrimSpace(diagnostic.ProcessingLog + "\n" + diagnostic.StderrTail)
		}
	}
	if _, err := m.DB.Exec(`UPDATE extraction_records SET status=?,finished=?,duration_ms=?,cached=?,frames=?,message=?,processing_log=? WHERE id=?`, status, finished.UTC().Format(time.RFC3339Nano), finished.Sub(started).Milliseconds(), cached, frames, extractionText(message), extractionText(detail), id); err != nil {
		slog.Error("记录抽帧结果失败", "record", id, "error", err)
	}
}

func (m *Manager) extractionFailure(run Run, entry int64, path string, cause error) {
	started := time.Now()
	id, err := m.startExtraction(run, entry, path, started)
	if err != nil {
		slog.Error("记录抽帧失败", "run", run.ID, "error", err)
		return
	}
	m.finishExtraction(id, started, false, 0, cause)
}

func extractionText(text string) string {
	const limit = 128 << 10
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "\n…日志已截断"
}
