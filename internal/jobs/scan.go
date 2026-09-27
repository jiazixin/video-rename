package jobs

import (
	"context"
	"time"

	"video-rename/internal/discovery"
	"video-rename/internal/library"
)

// queueFiles commits one bounded page of candidates and its counters together.
// Path display queries finish before acquiring the single writer connection.
func (m *Manager) queueFiles(ctx context.Context, batch Run, entries []library.Entry) error {
	type candidate struct {
		entry library.Entry
		path  string
	}
	files := []candidate{}
	parents := map[int64]string{}
	for _, e := range entries {
		if e.Kind != "file" || !discovery.IsVideoExtension(e.Name) {
			continue
		}
		prefix, ok := parents[e.Parent]
		if !ok {
			if e.Parent != 1 {
				prefix = m.Library.DisplayPath(e.Parent) + " / "
			}
			parents[e.Parent] = prefix
		}
		files = append(files, candidate{e, prefix + e.Name})
	}
	if len(files) == 0 {
		return ctx.Err()
	}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO run_items(run,entry,identity,size,mtime,original_path,status,message,finished_ms) VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	var discovered, skipped int64
	for _, f := range files {
		e := f.entry
		status, message := "queued", ""
		var finished int64
		if e.Processed != "" && batch.Task != "extract" {
			status, message, finished = "skipped", "已处理，跳过", time.Now().UnixMilli()
		}
		res, err := stmt.ExecContext(ctx, batch.ID, e.ID, e.Identity, e.Size, e.Mtime, f.path, status, message, finished)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		discovered += n
		if status == "skipped" {
			skipped += n
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET discovered=discovered+?,skipped=skipped+? WHERE id=?", discovered, skipped, batch.ID); err != nil {
		return err
	}
	return tx.Commit()
}
