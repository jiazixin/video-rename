package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/pipeline"
	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

func recoverExtractions(ctx context.Context, db *storage.Store) error {
	now := storage.Now()
	_, err := storage.WriteValue(ctx, db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, `UPDATE extraction_records SET status='failure',finished=?,duration_ms=MAX(0,CAST((julianday(?)-julianday(started))*86400000 AS INTEGER)),message=? WHERE status='running'`, now, now, recordExtractInterrupted)
	})
	return err
}

// prepareFramesOptions groups the inputs for prepareFrames.
type prepareFramesOptions struct {
	Run       Run
	Entry     int64
	Config    *config.TaskConfig
	Job       domain.VideoJob
	Probe     string
	Ffmpeg    string
	Extracted func()
}

// The record ends when usable frames are ready, independently of inference and rename.
// Cache hits are explicit so their short duration is not mistaken for a fresh extraction.
func (m *Manager) prepareFrames(ctx context.Context, params prepareFramesOptions) (*framecache.Lease, error) {
	run, entry, cfg, job, probe, ffmpeg, extracted := params.Run, params.Entry, params.Config, params.Job, params.Probe, params.Ffmpeg, params.Extracted

	// A pause interrupts capacity waits, but never an extraction already started.
	release := extracted
	defer func() { release() }()
	for {
		schedule, done := m.scheduleContext(ctx, run.ID)
		lease, err := m.prepareFramesOnce(ctx, schedule, prepareFramesOnceOptions{Run: run, Entry: entry, Config: cfg, Job: job, Probe: probe, Ffmpeg: ffmpeg})
		paused := schedule.Err() != nil
		done()
		if err == nil || !errors.Is(err, context.Canceled) || !paused || ctx.Err() != nil {
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

// prepareFramesOnceOptions groups the inputs for prepareFramesOnce.
type prepareFramesOnceOptions struct {
	Run    Run
	Entry  int64
	Config *config.TaskConfig
	Job    domain.VideoJob
	Probe  string
	Ffmpeg string
}

func (m *Manager) prepareFramesOnce(ctx context.Context, schedule context.Context, params prepareFramesOnceOptions) (lease *framecache.Lease, err error) {
	run, entry, cfg, job, probe, ffmpeg := params.Run, params.Entry, params.Config, params.Job, params.Probe, params.Ffmpeg

	started := time.Now()
	var id int64
	cached := true
	start := func() error {
		var err error
		id, err = m.startExtraction(ctx, startExtractionOptions{Run: run, Entry: entry, Path: m.Library.DisplayPath(ctx, entry), Started: started})
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
		m.finishExtraction(ctx, finishExtractionOptions{ID: id, Started: started, Cached: cached && err == nil, Frames: frames, ExtractionErr: err})
	}()
	reserve := int64(cfg.Sampling.MaxFrames)*(1<<20) + (32 << 20) + 65536
	lease, err = m.Cache.Acquire(schedule, framecache.Key(job, cfg), reserve, func(work string) (domain.FrameSet, error) {
		// The slot was admitted before pause; only cancellation stops its work.
		cached = false
		if err := start(); err != nil {
			return domain.FrameSet{}, fmt.Errorf("record extraction start: %w", err)
		}
		return m.prepare(ctx, pipeline.PrepareRequest{Config: cfg, Job: job, ProbePath: probe, FFmpegPath: ffmpeg, WorkDir: work})
	})
	if id == 0 && (err == nil || schedule.Err() == nil || ctx.Err() != nil) {
		if recordErr := start(); recordErr != nil {
			if lease != nil {
				lease.Release(false)
				lease = nil
			}
			return nil, fmt.Errorf("record extraction start: %w", recordErr)
		}
	}
	if err == nil {
		var current domain.Snapshot
		current, err = domain.StatSnapshot(job.Path)
		if err == nil && !current.Same(job.Snapshot) {
			err = domain.SourceChangedError(domain.SourceChange{Message: "video changed during extraction; extract again", Stage: domain.StageAfterExtract, Path: job.Path, Before: job.Snapshot, After: current})
		}
		if err != nil {
			lease.Release(true)
			lease = nil
		}
	}
	return
}

// startExtractionOptions groups the inputs for startExtraction.
type startExtractionOptions struct {
	Run     Run
	Entry   int64
	Path    string
	Started time.Time
}

func (m *Manager) startExtraction(ctx context.Context, params startExtractionOptions) (int64, error) {
	run, entry, path, started := params.Run, params.Entry, params.Path, params.Started

	source := "automatic"
	if run.Task == "extract" {
		source = "manual"
	}
	res, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, `INSERT INTO extraction_records(run,entry,path,source,status,started) VALUES(?,?,?,?,'running',?)`, run.ID, entry, path, source, timefmt.Format(started))
	})
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// finishExtractionOptions groups the inputs for finishExtraction.
type finishExtractionOptions struct {
	ID            int64
	Started       time.Time
	Cached        bool
	Frames        int
	ExtractionErr error
}

func (m *Manager) finishExtraction(ctx context.Context, params finishExtractionOptions) {
	id, started, cached, frames, extractionErr := params.ID, params.Started, params.Cached, params.Frames, params.ExtractionErr

	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	finished := time.Now()
	status, message, detail := "success", "", ""
	if extractionErr != nil {
		status, message = "failure", domain.RecordText(extractionErr)
		if errors.Is(extractionErr, context.Canceled) {
			message = recordExtractionCancelled
		}
		if diagnostic, ok := errors.AsType[*domain.JobError](extractionErr); ok {
			detail = strings.TrimSpace(diagnostic.ProcessingLog + "\n" + diagnostic.StderrTail)
		}
	}
	if _, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, `UPDATE extraction_records SET status=?,finished=?,duration_ms=?,cached=?,frames=?,message=?,processing_log=? WHERE id=?`, status, timefmt.Format(finished), finished.Sub(started).Milliseconds(), cached, frames, extractionText(message), extractionText(detail), id)
	}); err != nil {
		m.logger.Error("extraction_result_save_failed", "record_id", id, "error", err)
	}
}

// extractionFailureOptions groups the inputs for extractionFailure.
type extractionFailureOptions struct {
	Run   Run
	Entry int64
	Path  string
	Cause error
}

func (m *Manager) extractionFailure(ctx context.Context, params extractionFailureOptions) {
	run, entry, path, cause := params.Run, params.Entry, params.Path, params.Cause

	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	started := time.Now()
	id, err := m.startExtraction(ctx, startExtractionOptions{Run: run, Entry: entry, Path: path, Started: started})
	if err != nil {
		m.logger.Error("extraction_failure_save_failed", "run_id", run.ID, "error", err)
		return
	}
	m.finishExtraction(ctx, finishExtractionOptions{ID: id, Started: started, Cached: false, Frames: 0, ExtractionErr: cause})
}

func extractionText(text string) string {
	const limit = 128 << 10
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "\n…log truncated"
}
