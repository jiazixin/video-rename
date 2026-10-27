package jobs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/library"
)

type item struct {
	ID, Entry int64
	Identity  string
	Size      int64
	Mtime     string
}

func (m *Manager) scan(ctx context.Context, batch Run, id int64) error {
	run := batch.ID
	if _, err := m.dispatchContext(ctx, run); err != nil {
		return err
	}
	e, err := m.Library.IndexedEntry(id)
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
			m.extractionFailure(batch, id, m.Library.DisplayPath(id), err)
		} else if er := m.recordFailure("scan", id, run, m.Library.DisplayPath(id), err); er != nil {
			return er
		}
		_, er := m.DB.Exec("UPDATE runs SET failed=failed+1 WHERE id=?", run)
		return er
	}
	var after int64
	for {
		if _, err := m.dispatchContext(ctx, run); err != nil {
			return err
		}
		rows, err := m.DB.Read().QueryContext(ctx, `SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime,
        COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),'')
        FROM entries e WHERE e.parent=? AND e.present=1 AND e.id>? ORDER BY e.id LIMIT 128`, id, after)
		if err != nil {
			return err
		}
		children := []library.Entry{}
		for rows.Next() {
			var c library.Entry
			if err = rows.Scan(&c.ID, &c.Parent, &c.Name, &c.Kind, &c.Identity, &c.Size, &c.Mtime, &c.Processed); err != nil {
				break
			}
			children = append(children, c)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
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

func (m *Manager) process(ctx context.Context, batch Run, it item, cfg *config.TaskConfig, extracted func()) (result error) {
	run := batch.ID
	if ctx.Err() != nil {
		return
	}
	defer m.clearStage(it.ID)
	finish := func(status, msg string) {
		if err := m.finishItem(run, it.ID, status, msg); err != nil {
			result = fmt.Errorf("save item result: %w", err)
		}
	}
	e, p, snap, skip, err := m.checkItem(ctx, batch, it)
	if skip != "" {
		if skip == recordSkipUnstable && batch.Task == "extract" {
			m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), errUnstable)
		}
		finish("skipped", skip)
		return
	}
	if err == nil {
		err = m.startItem(it)
	}
	var committed, extractionLogged bool
	if err == nil {
		var lease *framecache.Lease
		lease, committed, extractionLogged, err = m.runItemTask(ctx, batch, it, e, p, snap, cfg, extracted)
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
				m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), err)
			}
		} else {
			rel, _ := filepath.Rel(m.Library.Root, p)
			if logErr := m.recordFailure("automatic", it.Entry, run, rel, err); logErr != nil {
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
			_, writeErr := m.DB.Exec("UPDATE runs SET error=? WHERE id=?", domain.RecordText(err), run)
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
		path, err = m.Library.Path(it.Entry)
	}
	if err == nil {
		snap, err = domain.StatSnapshot(path)
	}
	if err == nil && (library.Identity(snap) != it.Identity || snap.Size != it.Size || snap.ModTime.UTC().Format(time.RFC3339Nano) != it.Mtime) {
		return e, path, snap, recordSkipUnstable, nil
	}
	return e, path, snap, "", err
}

// startItem marks the item running and confirms its file is not held by
// another operation.
func (m *Manager) startItem(it item) error {
	if _, err := m.DB.Exec("UPDATE run_items SET status='running',started_ms=? WHERE id=?", time.Now().UnixMilli(), it.ID); err != nil {
		return err
	}
	m.setStage(it.ID, "extracting")
	return m.Library.CheckAvailable(it.Entry)
}

// runItemTask extracts frames, or names and commits the file. The caller
// releases the returned lease once the outcome is recorded.
func (m *Manager) runItemTask(ctx context.Context, batch Run, it item, e library.Entry, path string, snap domain.Snapshot, cfg *config.TaskConfig, extracted func()) (lease *framecache.Lease, committed, extractionLogged bool, err error) {
	probe, ffmpeg, err := m.Tools()
	if err != nil {
		m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), err)
		return nil, false, true, err
	}
	job := domain.VideoJob{Path: path, Snapshot: snap, JobID: fmt.Sprint(it.ID)}
	if batch.Task == "extract" {
		lease, err = m.prepareFrames(ctx, batch, it.Entry, cfg, job, probe, ffmpeg, extracted)
		extracted()
		if lease != nil {
			lease.Release(false)
		}
		return nil, false, true, err
	}
	var title string
	var exchange domain.ModelDiagnostics
	title, lease, err = m.analyze(domain.WithModelDiagnostics(ctx, &exchange), batch, it.Entry, cfg, job, probe, ffmpeg, extracted)
	if err != nil || ctx.Err() != nil {
		return lease, false, false, err
	}
	m.setStage(it.ID, "committing")
	requestKey := fmt.Sprintf("run:%d:item:%d", batch.ID, it.ID)
	if operation := batch.retry.RetryOperations[e.ID]; operation > 0 {
		_, err = m.Library.RetryAutoCommit(ctx, e.ID, snap, title, requestKey, batch.ID, operation, exchange)
	} else {
		_, err = m.Library.AutoCommit(ctx, e.ID, snap, title, requestKey, batch.ID, exchange)
	}
	return lease, err == nil, false, err
}
