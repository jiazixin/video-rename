package library

import (
	"context"
	"database/sql"
	"path/filepath"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// Numbering is a numbering plan and its state.
type Numbering struct {
	ID            string          `json:"id"`
	Parent        int64           `json:"parent"`
	Status        string          `json:"status"`
	Created       string          `json:"created"`
	UndoOf        string          `json:"undo_of"`
	Error         domain.Record   `json:"error,omitzero"`
	ProcessingLog string          `json:"processing_log"`
	Total         int             `json:"total"`
	Changed       int             `json:"changed"`
	Items         []NumberingItem `json:"items,omitzero"`
	Fingerprint   string          `json:"-"`
	Media         string          `json:"-"`
}

// Numbering returns numbering id.
func (l *Library) Numbering(ctx context.Context, id string) (Numbering, error) {
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) (Numbering, error) {
		var b Numbering
		var stored string
		err := tx.QueryRowContext(ctx, "SELECT id,parent,status,created,fingerprint,undo_of,error,media FROM image_numberings WHERE id=?", id).Scan(&b.ID, &b.Parent, &b.Status, &b.Created, &b.Fingerprint, &b.UndoOf, &stored, &b.Media)
		if err != nil {
			return b, err
		}
		if err = b.decodeNumberingError(stored); err != nil {
			return b, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT entry,version,identity,size,mtime_ns,old_name,new_name,temp_name,current_name,pending_name,operation,undo_of FROM image_numbering_items WHERE batch=? ORDER BY position`, id)
		if err != nil {
			return b, err
		}
		b.Items, err = storage.CollectRows(rows, func(rows *sql.Rows) (NumberingItem, error) {
			var i NumberingItem
			err := rows.Scan(&i.Entry, &i.Version, &i.Identity, &i.Size, &i.ModifiedNS, &i.Old, &i.New, &i.Temp, &i.Current, &i.Pending, &i.Operation, &i.UndoOf)
			return i, err
		})
		if err != nil {
			return b, err
		}
		for _, i := range b.Items {
			if i.Old != i.New {
				b.Changed++
			}
		}
		b.Total = len(b.Items)
		if b.Total == 0 {
			b.Items = nil
		}
		return b, nil
	})
}

// NumberingHistory returns the numberings of media under parent.
func (l *Library) NumberingHistory(ctx context.Context, parent int64, media string) ([]Numbering, error) {
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) ([]Numbering, error) {
		if _, err := mediaOf(media); err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT b.id,b.parent,b.status,b.created,b.undo_of,b.error,count(i.entry),COALESCE(sum(i.old_name<>i.new_name),0) FROM image_numberings b JOIN image_numbering_items i ON i.batch=b.id WHERE b.parent=? AND b.media=? AND b.status<>'preview' GROUP BY b.id ORDER BY b.created DESC,b.id DESC LIMIT 50`, parent, media)
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, func(rows *sql.Rows) (Numbering, error) {
			var b Numbering
			var stored string
			if err := rows.Scan(&b.ID, &b.Parent, &b.Status, &b.Created, &b.UndoOf, &stored, &b.Total, &b.Changed); err != nil {
				return b, err
			}
			err := b.decodeNumberingError(stored)
			return b, err
		})
	})
}

// PreviewNumberingRestore previews undoing numbering id.
func (l *Library) PreviewNumberingRestore(ctx context.Context, id string) (Numbering, error) {
	if err := l.lockNumbering(ctx); err != nil {
		return Numbering{}, err
	}
	defer l.mu.Unlock()
	original, err := l.Numbering(ctx, id)
	if err != nil {
		return Numbering{}, err
	}
	if original.Status != "committed" || original.UndoOf != "" {
		return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingNotRestorable, Message: "numbering batch cannot be restored"})
	}
	if err = l.CheckAvailable(ctx, original.Parent); err != nil {
		return Numbering{}, err
	}
	path, err := l.Path(ctx, original.Parent)
	if err != nil {
		return Numbering{}, err
	}
	fingerprint, err := directoryFingerprint(ctx, path)
	if err != nil {
		return Numbering{}, err
	}
	b := Numbering{ID: numberingToken(), Parent: original.Parent, Created: storage.Now(), Status: "preview", Fingerprint: fingerprint, UndoOf: id, Media: original.Media}
	for _, i := range original.Items {
		if err := ctx.Err(); err != nil {
			return Numbering{}, err
		}
		if i.Old == i.New {
			continue
		}
		e, err := l.Get(ctx, i.Entry)
		if err != nil {
			return Numbering{}, err
		}
		if e.Parent != b.Parent || e.Name != i.New || e.RestoreID != i.Operation || !numberingMatch(filepath.Join(path, e.Name), i) {
			return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingItemChangedSince, Message: "file changed or was renamed later; the batch cannot be restored", Details: map[string]any{"media": b.Media, "name": i.New}})
		}
		b.Items = append(b.Items, NumberingItem{Entry: e.ID, Version: e.Version, Identity: i.Identity, Size: i.Size, ModifiedNS: i.ModifiedNS, Old: i.New, New: i.Old, UndoOf: i.Operation})
	}
	if len(b.Items) == 0 {
		return Numbering{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingNothingToRestore, Message: "batch has nothing to restore", Details: map[string]any{"media": b.Media}})
	}
	return l.saveNumberingPreview(ctx, b, path)
}
