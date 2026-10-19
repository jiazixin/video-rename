package library

import (
	"context"
	"database/sql"
	"fmt"
	"video-rename/internal/discovery"
	"video-rename/internal/domain"
)

type MarkFailure struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}
type MarkResult struct {
	Changed     int64         `json:"changed"`
	Unchanged   int64         `json:"unchanged"`
	Failed      int           `json:"failed"`
	Failures    []MarkFailure `json:"failures"`
	Interrupted bool          `json:"interrupted"`
}

const markBatchSize = 128

// SetProcessed walks current disk contents, not just previously indexed children.
// Bounded batches are committed independently; failure details expose partial success.
func (l *Library) SetProcessed(ctx context.Context, ids []int64, marked bool) MarkResult {
	return l.SetProcessedWithProgress(ctx, ids, marked, nil)
}

// SetProcessedWithProgress reports committed counts while walking the targets.
// The callback runs synchronously and must not mutate the result's failure slice.
func (l *Library) SetProcessedWithProgress(ctx context.Context, ids []int64, marked bool, progress func(MarkResult)) (result MarkResult) {
	result = MarkResult{Failures: []MarkFailure{}}
	report := func() {
		if progress != nil {
			progress(result)
		}
	}
	defer func() {
		// Cancellation can happen between pages or targets, without a failed file.
		// Never present that partial traversal as a completed operation.
		result.Interrupted = ctx.Err() != nil
		report()
	}()
	seen := map[int64]bool{}
	addFailure := func(id int64, err error) {
		result.Failed++
		if len(result.Failures) < 100 {
			result.Failures = append(result.Failures, MarkFailure{id, l.DisplayPath(id), err.Error()})
		}
	}
	fail := func(id int64, err error) {
		addFailure(id, err)
		report()
	}
	pending := make([]int64, 0, markBatchSize)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		files := l.setFilesProcessed(ctx, pending, marked)
		pending = pending[:0]
		// The whole batch is durable before exposing any of its counts.
		for _, file := range files {
			if file.err != nil {
				addFailure(file.id, file.err)
			} else if file.changed > 0 {
				result.Changed += file.changed
			} else {
				result.Unchanged++
			}
		}
		report()
	}
	var walk func(Entry)
	walk = func(e Entry) {
		if seen[e.ID] {
			return
		}
		seen[e.ID] = true
		if err := ctx.Err(); err != nil {
			fail(e.ID, err)
			return
		}
		if e.Kind == "file" && (discovery.IsVideoExtension(e.Name) || IsImage(e.Name) || IsAudio(e.Name)) {
			pending = append(pending, e.ID)
			if len(pending) == markBatchSize {
				flush()
			}
			return
		}
		if e.Kind != "directory" {
			return
		}
		// Do not keep a partial batch waiting through another directory scan.
		flush()
		if ctx.Err() != nil {
			return
		}
		if err := l.Refresh(ctx, e.ID); err != nil {
			fail(e.ID, err)
			return
		}
		var after int64
		for ctx.Err() == nil {
			rows, err := l.DB.Read().QueryContext(ctx, "SELECT id,parent,name,kind FROM entries WHERE parent=? AND present=1 AND id>? ORDER BY id LIMIT ?", e.ID, after, markBatchSize)
			if err != nil {
				fail(e.ID, err)
				return
			}
			children := make([]Entry, 0, markBatchSize)
			for rows.Next() {
				var child Entry
				if err = rows.Scan(&child.ID, &child.Parent, &child.Name, &child.Kind); err != nil {
					break
				}
				children = append(children, child)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				fail(e.ID, err)
				return
			}
			if len(children) == 0 {
				flush()
				return
			}
			for _, child := range children {
				walk(child)
				after = child.ID
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		var e Entry
		err := l.DB.Read().QueryRowContext(ctx, "SELECT id,parent,name,kind FROM entries WHERE id=? AND present=1", id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind)
		if err != nil {
			seen[id] = true
			fail(id, err)
		} else {
			walk(e)
		}
		if ctx.Err() != nil {
			break
		}
	}
	flush()
	return result
}

type fileMarkResult struct {
	id      int64
	entry   Entry
	changed int64
	err     error
}

func (l *Library) setFilesProcessed(ctx context.Context, ids []int64, marked bool) (files []fileMarkResult) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files = make([]fileMarkResult, len(ids))
	for i, id := range ids {
		files[i].id = id
	}
	batchFailed := func(err error) {
		for i := range files {
			if files[i].err == nil {
				files[i].changed = 0
				files[i].err = fmt.Errorf("标记批次未提交: %w", err)
			}
		}
	}
	valid := 0
	for i := range files {
		if ctx.Err() != nil {
			break
		}
		files[i].entry, files[i].err = l.validateProcessedFile(files[i].id)
		if files[i].err == nil {
			valid++
		}
	}
	if err := ctx.Err(); err != nil {
		batchFailed(err)
		return
	}
	if valid == 0 {
		return
	}
	// All filesystem and write-pool queries finish before taking the sole
	// writer connection. Keep only SQL in this short transaction.
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		batchFailed(err)
		return
	}
	defer tx.Rollback()
	statements := make(map[string]*sql.Stmt)
	defer func() {
		for _, statement := range statements {
			statement.Close()
		}
	}()
	for i := range files {
		file := &files[i]
		if file.err != nil {
			continue
		}
		query := processedQuery(file.entry, marked)
		statement := statements[query]
		if statement == nil {
			statement, err = tx.PrepareContext(ctx, query)
			if err != nil {
				batchFailed(err)
				return
			}
			statements[query] = statement
		}
		res, err := statement.ExecContext(ctx, file.entry.Identity, file.entry.Size, file.entry.Mtime)
		if err != nil {
			batchFailed(err)
			return
		}
		file.changed, err = res.RowsAffected()
		if err != nil {
			batchFailed(err)
			return
		}
	}
	if err = ctx.Err(); err != nil {
		batchFailed(err)
		return
	}
	if err = tx.Commit(); err != nil {
		batchFailed(err)
		return
	}
	return
}

func (l *Library) validateProcessedFile(id int64) (Entry, error) {
	if err := l.CheckAvailable(id); err != nil {
		return Entry{}, err
	}
	if l.Busy != nil && l.Busy(id) {
		return Entry{}, fmt.Errorf("文件有活动任务，请等待任务结束后修改标记")
	}
	e, err := l.indexedSnapshot(id)
	if err != nil {
		return Entry{}, err
	}
	if e.Kind != "file" || !(discovery.IsVideoExtension(e.Name) || IsImage(e.Name) || IsAudio(e.Name)) {
		return Entry{}, fmt.Errorf("文件类型已变化，请刷新后重试")
	}
	p, err := l.pathForEntry(e)
	if err != nil {
		return Entry{}, err
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		return Entry{}, err
	}
	if Identity(snap) != e.Identity || snap.Size != e.Size || stamp(snap) != e.Mtime {
		return Entry{}, fmt.Errorf("文件已变化，请刷新后重试")
	}
	return e, nil
}

func processedQuery(e Entry, marked bool) string {
	query := "DELETE FROM marks WHERE identity=? AND size=? AND mtime=?"
	if marked {
		query = "INSERT INTO marks(identity,size,mtime,source,operation) VALUES(?,?,?,'marked',NULL) ON CONFLICT(identity,size,mtime) DO NOTHING"
		// Numbering also writes a manual processing state. An explicit media
		// mark must replace it so users can protect an already numbered file.
		if IsImage(e.Name) || IsAudio(e.Name) {
			query = "INSERT INTO marks(identity,size,mtime,source,operation) VALUES(?,?,?,'marked',NULL) ON CONFLICT(identity,size,mtime) DO UPDATE SET source='marked',operation=NULL WHERE source<>'marked'"
		}
	}
	return query
}
