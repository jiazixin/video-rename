package library

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"

	"video-rename/internal/discovery"
	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// markBatchSnapshot contains only this batch's indexed entries and ancestors.
// It is loaded after Busy callbacks, while Library.mu excludes mutations and
// scan publication. The read transaction closes before any filesystem I/O.
type markBatchSnapshot map[int64]Entry

func (s markBatchSnapshot) lookup(ctx context.Context, id int64) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	e, ok := s[id]
	if !ok {
		return Entry{}, sql.ErrNoRows
	}
	return e, nil
}

func (l *Library) readMarkBatch(ctx context.Context, files []fileMarkResult) (markBatchSnapshot, error) {
	ids := make([]int64, len(files))
	for i := range files {
		ids[i] = files[i].id
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	snapshot := make(markBatchSnapshot, len(files)+1)
	err = l.db.View(ctx, func(tx *sql.Tx) (err error) {
		pending, err := readPendingOperations(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,parent,name,kind,identity,size,mtime_ns,version
 FROM entries WHERE id IN (SELECT value FROM json_each(?)) AND present=1`, string(raw))
		if err != nil {
			return err
		}
		entries, err := storage.CollectRows(rows, func(rows *sql.Rows) (e Entry, err error) {
			err = rows.Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.ModifiedNS, &e.Version)
			return e, err
		})
		if err != nil {
			return err
		}
		for _, e := range entries {
			snapshot[e.ID] = e
		}
		statement, err := tx.PrepareContext(ctx, `SELECT id,parent,name,kind,identity,size,mtime_ns,version
 FROM entries WHERE id=? AND present=1`)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, statement.Close()) }()
		for i := range files {
			file := &files[i]
			// Availability errors retain precedence over the Busy result.
			if err := pending.check(ctx, tx, file.id); err != nil {
				file.err = err
			}
			if file.err != nil {
				continue
			}
			file.entry, file.err = snapshot.lookup(ctx, file.id)
			if file.err == nil && !isMarkableFile(file.entry) {
				file.err = errChanged
			}
			if file.err == nil {
				file.err = snapshot.loadAncestors(ctx, statement, file.entry)
			}
		}
		return ctx.Err()
	})
	return snapshot, err
}

func (s markBatchSnapshot) loadAncestors(ctx context.Context, statement *sql.Stmt, e Entry) error {
	seen := map[int64]bool{}
	for e.ID != 1 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.ID <= 0 || e.Parent <= 0 || seen[e.ID] || len(seen) > 1024 {
			return errBadHierarchy
		}
		seen[e.ID] = true
		parent, ok := s[e.Parent]
		if !ok {
			err := statement.QueryRowContext(ctx, e.Parent).Scan(&parent.ID, &parent.Parent, &parent.Name, &parent.Kind, &parent.Identity, &parent.Size, &parent.ModifiedNS, &parent.Version)
			if err != nil {
				return err
			}
			s[parent.ID] = parent
		}
		e = parent
	}
	return nil
}

func isMarkableFile(e Entry) bool {
	return e.Kind == "file" && (discovery.IsVideoExtension(e.Name) || IsImage(e.Name) || IsAudio(e.Name))
}

func (l *Library) validateProcessedSnapshot(ctx context.Context, e Entry, snapshot markBatchSnapshot) error {
	p, err := l.pathForSnapshot(ctx, e, snapshot.lookup)
	if err != nil {
		return err
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		return err
	}
	if Identity(snap) != e.Identity || snap.Size != e.Size || stamp(snap) != e.ModifiedNS {
		return errChanged
	}
	return nil
}
