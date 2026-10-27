package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"video-rename/internal/domain"
)

// Resolve a crash between filesystem rename and journal completion without guessing.
func (l *Library) reconcileNumbering(ctx context.Context, b *Numbering, path string) error {
	names, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	exact := map[string]bool{}
	for _, n := range names {
		exact[n.Name()] = true
	}
	for idx := range b.Items {
		i := &b.Items[idx]
		current := exact[i.Current] && numberingMatch(filepath.Join(path, i.Current), *i)
		if i.Pending != "" {
			pending := exact[i.Pending] && numberingMatch(filepath.Join(path, i.Pending), *i)
			if current == pending {
				return domain.Fail(domain.KindInternal, "numbering_item_ambiguous", "file location is ambiguous; manual review needed", "media", b.Media, "name", i.Old)
			}
			if pending {
				i.Current = i.Pending
			}
			if _, err = l.DB.ExecContext(ctx, "UPDATE image_numbering_items SET current_name=?,pending_name='' WHERE batch=? AND entry=?", i.Current, b.ID, i.Entry); err != nil {
				return err
			}
			i.Pending = ""
		} else if !current {
			return domain.Fail(domain.KindInternal, "numbering_item_modified", "file was modified externally; manual review needed", "media", b.Media, "name", i.Old)
		}
	}
	return syncNumberingDir(path)
}
func (l *Library) rollbackNumbering(ctx context.Context, b *Numbering, path string) error {
	if _, err := l.DB.ExecContext(ctx, "UPDATE image_numberings SET status='rollback' WHERE id=?", b.ID); err != nil {
		return err
	}
	b.Status = "rollback"
	if err := l.reconcileNumbering(ctx, b, path); err != nil {
		return err
	}
	// Stage first, including final names that may occupy another item's original name.
	for idx := range b.Items {
		i := &b.Items[idx]
		if i.Current != i.Old {
			if err := l.moveNumbering(ctx, b, i, path, i.Temp); err != nil {
				return err
			}
		}
	}
	for idx := range b.Items {
		i := &b.Items[idx]
		if i.Current != i.Old {
			if err := l.moveNumbering(ctx, b, i, path, i.Old); err != nil {
				return err
			}
		}
	}
	return nil
}
func (l *Library) recoverNumberings(ctx context.Context) error {
	rows, err := l.DB.QueryContext(ctx, "SELECT id FROM image_numberings WHERE status IN ('running','rollback','review') ORDER BY created")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = l.recoverNumbering(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// RecoverNumbering resumes or rolls back the interrupted numbering id.
func (l *Library) RecoverNumbering(ctx context.Context, id string) (Numbering, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recoverNumbering(ctx, id)
}
func (l *Library) recoverNumbering(ctx context.Context, id string) (Numbering, error) {
	b, err := l.Numbering(ctx, id)
	if err != nil {
		return b, err
	}
	if b.Status != "running" && b.Status != "rollback" && b.Status != "review" {
		return b, nil
	}
	path, err := l.Path(ctx, b.Parent)
	if err == nil {
		err = l.rollbackNumbering(ctx, &b, path)
	}
	b.Status = "rolled_back"
	b.Error = domain.Record{Code: "numbering_interrupted_restored", Message: "interrupted numbering was rolled back", Details: map[string]any{"media": b.Media}}
	if err != nil {
		b.Status = "review"
		b.Error = domain.Record{Code: "numbering_recovery_incomplete", Message: "numbering recovery incomplete: " + err.Error(), Details: map[string]any{"media": b.Media, "recovery": domain.RecordOf(err)}}
		if diagnostic, ok := errors.AsType[*domain.JobError](err); ok {
			b.ProcessingLog += "\nrecovery failure:\n" + diagnostic.ProcessingLog
		}
	}
	_, dbErr := l.DB.ExecContext(ctx, "UPDATE image_numberings SET status=?,error=? WHERE id=?", b.Status, b.numberingErrorRecord(), id)
	return b, dbErr
}
