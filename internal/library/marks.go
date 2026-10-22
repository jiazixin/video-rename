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
	w := &markWalk{l: l, ctx: ctx, marked: marked, progress: progress, result: MarkResult{Failures: []MarkFailure{}}, seen: map[int64]bool{}, pending: make([]int64, 0, markBatchSize)}
	defer func() {
		// Cancellation can happen between pages or targets, without a failed file.
		// Never present that partial traversal as a completed operation.
		w.result.Interrupted = ctx.Err() != nil
		w.report()
		result = w.result
	}()
	for _, id := range ids {
		if w.seen[id] {
			continue
		}
		var e Entry
		err := l.DB.Read().QueryRowContext(ctx, "SELECT id,parent,name,kind FROM entries WHERE id=? AND present=1", id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind)
		if err != nil {
			w.seen[id] = true
			w.fail(id, err)
		} else {
			w.walk(e)
		}
		if ctx.Err() != nil {
			break
		}
	}
	w.flush()
	return w.result
}

// markWalk is one SetProcessed traversal. Media files collect into batches of
// markBatchSize; each batch commits before its counts become visible.
type markWalk struct {
	l        *Library
	ctx      context.Context
	marked   bool
	progress func(MarkResult)
	result   MarkResult
	seen     map[int64]bool
	pending  []int64
}

func (w *markWalk) report() {
	if w.progress != nil {
		w.progress(w.result)
	}
}

func (w *markWalk) addFailure(id int64, err error) {
	w.result.Failed++
	if len(w.result.Failures) < 100 {
		w.result.Failures = append(w.result.Failures, MarkFailure{id, w.l.DisplayPath(id), err.Error()})
	}
}

func (w *markWalk) fail(id int64, err error) {
	w.addFailure(id, err)
	w.report()
}

func (w *markWalk) flush() {
	if len(w.pending) == 0 {
		return
	}
	files := w.l.setFilesProcessed(w.ctx, w.pending, w.marked)
	w.pending = w.pending[:0]
	// The whole batch is durable before exposing any of its counts.
	for _, file := range files {
		if file.err != nil {
			w.addFailure(file.id, file.err)
		} else if file.changed > 0 {
			w.result.Changed += file.changed
		} else {
			w.result.Unchanged++
		}
	}
	w.report()
}

func (w *markWalk) walk(e Entry) {
	if w.seen[e.ID] {
		return
	}
	w.seen[e.ID] = true
	if err := w.ctx.Err(); err != nil {
		w.fail(e.ID, err)
		return
	}
	if e.Kind == "file" && (discovery.IsVideoExtension(e.Name) || IsImage(e.Name) || IsAudio(e.Name)) {
		w.pending = append(w.pending, e.ID)
		if len(w.pending) == markBatchSize {
			w.flush()
		}
		return
	}
	if e.Kind == "directory" {
		w.walkDirectory(e)
	}
}

// walkDirectory refreshes a directory from disk, then walks its children page
// by page.
func (w *markWalk) walkDirectory(e Entry) {
	// Do not keep a partial batch waiting through another directory scan.
	w.flush()
	if w.ctx.Err() != nil {
		return
	}
	if err := w.l.Refresh(w.ctx, e.ID); err != nil {
		w.fail(e.ID, err)
		return
	}
	var after int64
	for w.ctx.Err() == nil {
		children, err := w.childPage(e.ID, after)
		if err != nil {
			w.fail(e.ID, err)
			return
		}
		if len(children) == 0 {
			w.flush()
			return
		}
		for _, child := range children {
			w.walk(child)
			after = child.ID
			if w.ctx.Err() != nil {
				return
			}
		}
	}
}

func (w *markWalk) childPage(parent, after int64) ([]Entry, error) {
	rows, err := w.l.DB.Read().QueryContext(w.ctx, "SELECT id,parent,name,kind FROM entries WHERE parent=? AND present=1 AND id>? ORDER BY id LIMIT ?", parent, after, markBatchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	children := make([]Entry, 0, markBatchSize)
	for rows.Next() {
		var child Entry
		if err = rows.Scan(&child.ID, &child.Parent, &child.Name, &child.Kind); err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, rows.Err()
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
