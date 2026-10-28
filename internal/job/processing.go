package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/library"
	"video-rename/internal/storage"
)

type item struct {
	ID, Entry  int64
	Identity   string
	Size       int64
	ModifiedNS int64
}

func (m *Manager) scan(ctx context.Context, batch Run, id int64) error {
	run := batch.ID
	if err := m.waitDispatch(ctx, run); err != nil {
		return err
	}
	e, err := m.Library.IndexedEntry(ctx, id)
	if err != nil {
		return err
	}
	if e.Kind == "file" {
		return m.queueFiles(ctx, batch, []library.Entry{e})
	}
	if e.Kind != "directory" {
		return nil
	}
	if err = m.Library.Refresh(ctx, id); err != nil {
		if batch.Task == "extract" {
			m.extractionFailure(ctx, extractionFailureOptions{Run: batch, Entry: id, Path: m.diagnosticPath(ctx, id), Cause: err})
		} else if err := m.recordFailure(ctx, recordFailureOptions{Action: "scan", Entry: id, Run: run, Path: m.diagnosticPath(ctx, id), Cause: err}); err != nil {
			return err
		}
		// The counter belongs to the failure just recorded.
		record, cancel := storage.Detach(ctx)
		defer cancel()
		_, err := storage.WriteValue(record, m.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(record, "UPDATE runs SET failed=failed+1 WHERE id=?", run)
		})
		return err
	}
	var after int64
	for {
		if err := m.waitDispatch(ctx, run); err != nil {
			return err
		}
		children := []library.Entry{}
		err := m.db.View(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime_ns,
        COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime_ns=e.mtime_ns),'')
        FROM entries e WHERE e.parent=? AND e.present=1 AND e.id>? ORDER BY e.id LIMIT 128`, id, after)
			if err != nil {
				return err
			}
			children, err = storage.CollectRows(rows, func(rows *sql.Rows) (library.Entry, error) {
				var c library.Entry
				err := rows.Scan(&c.ID, &c.Parent, &c.Name, &c.Kind, &c.Identity, &c.Size, &c.ModifiedNS, &c.Processed)
				return c, err
			})
			return err
		})
		if err != nil {
			return err
		}
		if len(children) == 0 {
			return nil
		}
		if err = m.queueFiles(ctx, batch, children); err != nil {
			return err
		}
		for _, c := range children {
			after = c.ID
			if batch.Recursive && c.Kind == "directory" {
				if err = m.scan(ctx, batch, c.ID); err != nil {
					return err
				}
			}
		}
	}
}

// processOptions groups the inputs for process.
type processOptions struct {
	Batch     Run
	It        item
	Config    *config.TaskConfig
	Extracted func()
}

func (m *Manager) process(ctx context.Context, params processOptions) (result error) {
	batch, it, cfg, extracted := params.Batch, params.It, params.Config, params.Extracted

	run := batch.ID
	if ctx.Err() != nil {
		return
	}
	defer m.clearStage(it.ID)
	finish := func(status, msg string) {
		if err := m.finishItem(ctx, finishItemOptions{Run: run, ID: it.ID, Status: status, Message: msg}); err != nil {
			result = fmt.Errorf("save item result: %w", err)
		}
	}
	e, p, snap, skip, err := m.checkItem(ctx, batch, it)
	if skip != "" {
		if skip == recordSkipUnstable && batch.Task == "extract" {
			m.extractionFailure(ctx, extractionFailureOptions{Run: batch, Entry: it.Entry, Path: m.diagnosticPath(ctx, it.Entry), Cause: errUnstable})
		}
		finish("skipped", skip)
		return
	}
	if err == nil {
		err = m.startItem(ctx, it)
	}
	var committed, extractionLogged bool
	if err == nil {
		var lease *framecache.Lease
		lease, committed, extractionLogged, err = m.runItemTask(ctx, runItemTaskOptions{Batch: batch, It: it, Entry: e, Path: p, Snap: snap, Config: cfg, Extracted: extracted})
		if lease != nil {
			// Released after the outcome is recorded; a committed title keeps its frames.
			defer func() { lease.Release(committed) }()
		}
	}
	if ctx.Err() != nil && !committed {
		finish("cancelled", recordCancelled)
		return
	}
	if err != nil {
		if batch.Task == "extract" {
			if !extractionLogged {
				m.extractionFailure(ctx, extractionFailureOptions{Run: batch, Entry: it.Entry, Path: m.diagnosticPath(ctx, it.Entry), Cause: err})
			}
		} else {
			rel, pathErr := filepath.Rel(m.Library.Root, p)
			if pathErr != nil {
				rel = m.diagnosticPath(ctx, it.Entry)
				err = errors.Join(err, fmt.Errorf("resolve diagnostic path for entry %d: %w", it.Entry, pathErr))
			}
			if logErr := m.recordFailure(ctx, recordFailureOptions{Action: "automatic", Entry: it.Entry, Run: run, Path: rel, Cause: err}); logErr != nil {
				m.logger.Error("video_diagnostics_save_failed", "run_id", run, "entry_id", it.Entry, "error", logErr)
			}
		}
		finish("failed", domain.RecordText(err))
		if je, ok := errors.AsType[*domain.JobError](err); (ok && je.Global) || m.Library.Halted() {
			m.mu.Lock()
			if r, ok := m.active[run]; ok {
				r.cancel()
			}
			m.mu.Unlock()
			// The cancel above ends ctx; the cause must still reach the run.
			record, cancel := storage.Detach(ctx)
			defer cancel()
			_, writeErr := storage.WriteValue(record, m.db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(record, "UPDATE runs SET error=? WHERE id=?", domain.RecordText(err), run)
			})
			result = errors.Join(result, writeErr)
		}
		return
	}
	finish("done", "")
	return
}

// checkItem resolves an item's file and confirms it is unchanged since
// discovery. A non-empty skip reason means the item is not processed.
func (m *Manager) checkItem(ctx context.Context, batch Run, it item) (e library.Entry, path string, snap domain.Snapshot, skip string, err error) {
	retryOperation := batch.retry.RetryOperations[it.Entry]
	e, err = m.Library.Get(ctx, it.Entry)
	if err == nil && e.Processed != "" && batch.Task != "extract" && retryOperation == 0 {
		return e, "", snap, recordSkipProcessed, nil
	}
	if err == nil && retryOperation > 0 {
		err = m.Library.CheckAutomaticRetry(ctx, it.Entry, retryOperation)
	}
	if err == nil {
		path, err = m.Library.Path(ctx, it.Entry)
	}
	if err == nil {
		snap, err = domain.StatSnapshot(path)
	}
	if err == nil && (library.Identity(snap) != it.Identity || snap.Size != it.Size || snap.ModTime.UnixNano() != it.ModifiedNS) {
		return e, path, snap, recordSkipUnstable, nil
	}
	return e, path, snap, "", err
}

// startItem marks the item running and confirms its file is not held by
// another operation.
func (m *Manager) startItem(ctx context.Context, it item) error {
	if _, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE run_items SET status='running',started_ms=? WHERE id=?", time.Now().UnixMilli(), it.ID)
	}); err != nil {
		return err
	}
	m.setStage(it.ID, "extracting")
	return m.Library.CheckAvailable(ctx, it.Entry)
}

// runItemTaskOptions groups the inputs for runItemTask.
type runItemTaskOptions struct {
	Batch     Run
	It        item
	Entry     library.Entry
	Path      string
	Snap      domain.Snapshot
	Config    *config.TaskConfig
	Extracted func()
}

// runItemTask extracts frames, or names and commits the file. The caller
// releases the returned lease once the outcome is recorded.
func (m *Manager) runItemTask(ctx context.Context, params runItemTaskOptions) (lease *framecache.Lease, committed, extractionLogged bool, err error) {
	batch, it, e, path, snap, cfg, extracted := params.Batch, params.It, params.Entry, params.Path, params.Snap, params.Config, params.Extracted

	probe, ffmpeg, err := m.Tools()
	if err != nil {
		m.extractionFailure(ctx, extractionFailureOptions{Run: batch, Entry: it.Entry, Path: m.diagnosticPath(ctx, it.Entry), Cause: err})
		return nil, false, true, err
	}
	job := domain.VideoJob{Path: path, Snapshot: snap, JobID: fmt.Sprint(it.ID)}
	if batch.Task == "extract" {
		lease, err = m.prepareFrames(ctx, prepareFramesOptions{Run: batch, Entry: it.Entry, Config: cfg, Job: job, Probe: probe, Ffmpeg: ffmpeg, Extracted: extracted})
		extracted()
		if lease != nil {
			lease.Release(false)
		}
		return nil, false, true, err
	}
	var title string
	var exchange domain.ModelDiagnostics
	title, lease, err = m.analyze(domain.WithModelDiagnostics(ctx, &exchange), analyzeOptions{Run: batch, Entry: it.Entry, Config: cfg, Job: job, Probe: probe, Ffmpeg: ffmpeg, Extracted: extracted})
	if err != nil || ctx.Err() != nil {
		return lease, false, false, err
	}
	m.setStage(it.ID, "committing")
	requestKey := fmt.Sprintf("run:%d:item:%d", batch.ID, it.ID)
	if operation := batch.retry.RetryOperations[e.ID]; operation > 0 {
		_, err = m.Library.RetryAutoCommit(ctx, library.RetryAutoCommitOptions{ID: e.ID, Snap: snap, Title: title, Key: requestKey, Run: batch.ID, Operation: operation, Diagnostics: []domain.ModelDiagnostics{exchange}})
	} else {
		_, err = m.Library.AutoCommit(ctx, library.AutoCommitOptions{ID: e.ID, Snap: snap, Title: title, Key: requestKey, Run: batch.ID, Diagnostics: []domain.ModelDiagnostics{exchange}})
	}
	return lease, err == nil, false, err
}
