package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	committed := false
	extractionLogged := false
	finish := func(status, msg string) {
		if err := m.finishItem(run, it.ID, status, msg); err != nil {
			result = fmt.Errorf("保存文件处理结果失败: %w", err)
		}
	}
	e, err := m.Library.Get(it.Entry)
	if err == nil && e.Processed != "" && batch.Task != "extract" && batch.retry.RetryOperations[it.Entry] == 0 {
		finish("skipped", "已处理，跳过")
		return
	}
	if err == nil && batch.retry.RetryOperations[it.Entry] > 0 {
		err = m.Library.CheckAutomaticRetry(it.Entry, batch.retry.RetryOperations[it.Entry])
	}
	var p string
	if err == nil {
		p, err = m.Library.Path(it.Entry)
	}
	var snap domain.Snapshot
	if err == nil {
		snap, err = domain.StatSnapshot(p)
	}
	if err == nil && (library.Identity(snap) != it.Identity || snap.Size != it.Size || snap.ModTime.UTC().Format(time.RFC3339Nano) != it.Mtime) {
		if batch.Task == "extract" {
			m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), fmt.Errorf("文件尚不稳定，留待下次运行"))
		}
		finish("skipped", "文件尚不稳定，留待下次运行")
		return
	}
	if err == nil {
		_, err = m.DB.Exec("UPDATE run_items SET status='running',started_ms=? WHERE id=?", time.Now().UnixMilli(), it.ID)
	}
	if err == nil {
		err = m.Library.CheckAvailable(it.Entry)
	}
	if err == nil {
		probe, ffmpeg, te := m.Tools()
		err = te
		if te != nil {
			m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), te)
			extractionLogged = true
		}
		if err == nil {
			if batch.Task == "extract" {
				var lease *framecache.Lease
				lease, err = m.prepareFrames(ctx, batch, it.Entry, cfg, domain.VideoJob{Path: p, Snapshot: snap, JobID: fmt.Sprint(it.ID)}, probe, ffmpeg, extracted)
				extractionLogged = true
				extracted()
				if lease != nil {
					lease.Release(false)
				}
			} else {
				var title string
				var exchange domain.ModelDiagnostics
				var lease *framecache.Lease
				title, lease, err = m.analyze(domain.WithModelDiagnostics(ctx, &exchange), batch, it.Entry, cfg, domain.VideoJob{Path: p, Snapshot: snap, JobID: fmt.Sprint(it.ID)}, probe, ffmpeg, extracted)
				if lease != nil {
					defer func() { lease.Release(committed) }()
				}
				if err == nil && ctx.Err() == nil {
					if operation := batch.retry.RetryOperations[e.ID]; operation > 0 {
						_, err = m.Library.RetryAutoCommit(e.ID, snap, title, fmt.Sprintf("run:%d:item:%d", run, it.ID), run, operation, exchange)
					} else {
						_, err = m.Library.AutoCommit(e.ID, snap, title, fmt.Sprintf("run:%d:item:%d", run, it.ID), run, exchange)
					}
					committed = err == nil
				}
			}
		}
	}
	if ctx.Err() != nil && !committed {
		finish("cancelled", "已取消")
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
				slog.Error("保存视频失败诊断失败", "run", run, "entry", it.Entry, "error", logErr)
			}
		}
		finish("failed", err.Error())
		if je, ok := errors.AsType[*domain.JobError](err); (ok && je.Global) || m.Library.Halted() {
			m.mu.Lock()
			if r, ok := m.active[run]; ok {
				r.cancel()
			}
			m.mu.Unlock()
			_, writeErr := m.DB.Exec("UPDATE runs SET error=? WHERE id=?", err.Error(), run)
			result = errors.Join(result, writeErr)
		}
		return
	}
	finish("done", "")
	return
}
