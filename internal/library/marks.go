package library

import (
	"context"
	"database/sql"
	"errors"

	"video-rename/internal/discovery"
	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// MarkFailure reports an entry whose mark could not be changed.
type MarkFailure struct {
	ID    int64         `json:"id"`
	Name  string        `json:"name"`
	Error domain.Record `json:"error"`
}

// MarkResult reports the outcome of changing marks.
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
	w := &markWalk{l: l, marked: marked, progress: progress, result: MarkResult{Failures: []MarkFailure{}}, seen: map[int64]bool{}, pending: make([]int64, 0, markBatchSize)}
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
		err := l.db.View(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT id,parent,name,kind FROM entries WHERE id=? AND present=1", id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind)
		})
		if err != nil {
			w.seen[id] = true
			w.fail(ctx, id, err)
		} else {
			w.walk(ctx, e)
		}
		if ctx.Err() != nil {
			break
		}
	}
	w.flush(ctx)
	return w.result
}

// markWalk is one SetProcessed traversal. Media files collect into batches of
// markBatchSize; each batch commits before its counts become visible.
type markWalk struct {
	l        *Library
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

func (w *markWalk) addFailure(ctx context.Context, id int64, err error) {
	w.result.Failed++
	if len(w.result.Failures) < 100 {
		w.result.Failures = append(w.result.Failures, MarkFailure{id, w.l.DisplayPath(ctx, id), domain.RecordOf(err)})
	}
}

func (w *markWalk) fail(ctx context.Context, id int64, err error) {
	w.addFailure(ctx, id, err)
	w.report()
}

func (w *markWalk) flush(ctx context.Context) {
	if len(w.pending) == 0 {
		return
	}
	files := w.l.setFilesProcessed(ctx, w.pending, w.marked)
	w.pending = w.pending[:0]
	// The whole batch is durable before exposing any of its counts.
	for _, file := range files {
		if file.err != nil {
			w.addFailure(ctx, file.id, file.err)
		} else if file.changed > 0 {
			w.result.Changed += file.changed
		} else {
			w.result.Unchanged++
		}
	}
	w.report()
}

func (w *markWalk) walk(ctx context.Context, e Entry) {
	if w.seen[e.ID] {
		return
	}
	w.seen[e.ID] = true
	if err := ctx.Err(); err != nil {
		w.fail(ctx, e.ID, err)
		return
	}
	if e.Kind == "file" && (discovery.IsVideoExtension(e.Name) || IsImage(e.Name) || IsAudio(e.Name)) {
		w.pending = append(w.pending, e.ID)
		if len(w.pending) == markBatchSize {
			w.flush(ctx)
		}
		return
	}
	if e.Kind == "directory" {
		w.walkDirectory(ctx, e)
	}
}

// walkDirectory refreshes a directory from disk, then walks its children page
// by page.
func (w *markWalk) walkDirectory(ctx context.Context, e Entry) {
	// Do not keep a partial batch waiting through another directory scan.
	w.flush(ctx)
	if ctx.Err() != nil {
		return
	}
	if err := w.l.Refresh(ctx, e.ID); err != nil {
		w.fail(ctx, e.ID, err)
		return
	}
	var after int64
	for ctx.Err() == nil {
		children, err := w.childPage(ctx, e.ID, after)
		if err != nil {
			w.fail(ctx, e.ID, err)
			return
		}
		if len(children) == 0 {
			w.flush(ctx)
			return
		}
		for _, child := range children {
			w.walk(ctx, child)
			after = child.ID
			if ctx.Err() != nil {
				return
			}
		}
	}
}

func (w *markWalk) childPage(ctx context.Context, parent, after int64) ([]Entry, error) {
	return storage.ReadValue(ctx, w.l.db, func(tx *sql.Tx) ([]Entry, error) {
		rows, err := tx.QueryContext(ctx, "SELECT id,parent,name,kind FROM entries WHERE parent=? AND present=1 AND id>? ORDER BY id LIMIT ?", parent, after, markBatchSize)
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, func(rows *sql.Rows) (Entry, error) {
			var child Entry
			err := rows.Scan(&child.ID, &child.Parent, &child.Name, &child.Kind)
			return child, err
		})
	})
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
				files[i].err = domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonMarkBatchFailed, Message: "mark batch not committed"}).Wrap(err)
			}
		}
	}
	valid := 0
	for i := range files {
		if ctx.Err() != nil {
			break
		}
		files[i].entry, files[i].err = l.validateProcessedFile(ctx, files[i].id)
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
	err := l.db.Write(ctx, func(tx *sql.Tx) (err error) {
		statements := make(map[string]*sql.Stmt)
		defer func() {
			for _, statement := range statements {
				err = errors.Join(err, statement.Close())
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
					return err
				}
				statements[query] = statement
			}
			res, err := statement.ExecContext(ctx, file.entry.Identity, file.entry.Size, file.entry.ModifiedNS)
			if err != nil {
				return err
			}
			file.changed, err = res.RowsAffected()
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		batchFailed(err)
	}
	return
}

func (l *Library) validateProcessedFile(ctx context.Context, id int64) (Entry, error) {
	if err := l.CheckAvailable(ctx, id); err != nil {
		return Entry{}, err
	}
	if l.Busy != nil && l.Busy(ctx, id) {
		return Entry{}, errBusy
	}
	e, err := l.indexedSnapshot(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if e.Kind != "file" || !(discovery.IsVideoExtension(e.Name) || IsImage(e.Name) || IsAudio(e.Name)) {
		return Entry{}, errChanged
	}
	p, err := l.pathForEntry(ctx, e)
	if err != nil {
		return Entry{}, err
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		return Entry{}, err
	}
	if Identity(snap) != e.Identity || snap.Size != e.Size || stamp(snap) != e.ModifiedNS {
		return Entry{}, errChanged
	}
	return e, nil
}

func processedQuery(e Entry, marked bool) string {
	query := "DELETE FROM marks WHERE identity=? AND size=? AND mtime_ns=?"
	if marked {
		query = "INSERT INTO marks(identity,size,mtime_ns,source,operation) VALUES(?,?,?,'marked',NULL) ON CONFLICT(identity,size,mtime_ns) DO NOTHING"
		// Numbering also writes a manual processing state. An explicit media
		// mark must replace it so users can protect an already numbered file.
		if IsImage(e.Name) || IsAudio(e.Name) {
			query = "INSERT INTO marks(identity,size,mtime_ns,source,operation) VALUES(?,?,?,'marked',NULL) ON CONFLICT(identity,size,mtime_ns) DO UPDATE SET source='marked',operation=NULL WHERE source<>'marked'"
		}
	}
	return query
}
