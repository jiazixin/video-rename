package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"video-rename/internal/domain"
	"video-rename/internal/rename"
	"video-rename/internal/storage"
)

// ApplyNumbering applies the previewed numbering id and reports progress;
// progress may be nil.
func (l *Library) ApplyNumbering(ctx context.Context, id string, progress func(int, int)) (Numbering, error) {
	if err := l.lockNumbering(ctx); err != nil {
		return Numbering{}, err
	}
	defer l.mu.Unlock()
	b, err := l.Numbering(ctx, id)
	if err != nil {
		return b, err
	}
	if b.Status == "committed" || b.Status == "restored" {
		return b, nil
	}
	if b.Status != "preview" {
		return b, domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_not_preview", Message: "numbering batch is not a preview", Details: map[string]any{"status": b.Status}})
	}
	if l.broken != nil {
		return b, errHalted
	}
	if err = l.CheckAvailable(ctx, b.Parent); err != nil {
		return b, err
	}
	if l.Busy != nil && l.Busy(ctx, b.Parent) {
		return b, domain.Fail(domain.KindConflict, domain.Record{Code: "descendant_busy", Message: "a descendant has an active job"})
	}
	path, err := l.Path(ctx, b.Parent)
	if err != nil {
		return b, err
	}
	fingerprint, err := directoryFingerprint(path)
	if err != nil {
		return b, err
	}
	if fingerprint != b.Fingerprint {
		return b, numberingFingerprintFailure(path, b, fingerprint)
	}
	if b.UndoOf != "" {
		original, err := l.Numbering(ctx, b.UndoOf)
		if err != nil {
			return b, err
		}
		if original.Status != "committed" {
			return b, domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_original_unavailable", Message: "original batch is restored or unavailable"})
		}
	}
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		e, err := l.Get(ctx, i.Entry)
		if err != nil {
			return b, err
		}
		if b.UndoOf == "" && e.Processed == "marked" {
			return b, domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_item_marked", Message: "file is marked to skip numbering; preview again", Details: map[string]any{"media": b.Media, "name": e.Name}})
		}
		if e.Parent != b.Parent || e.Version != i.Version || e.Name != i.Old || !numberingMatch(filepath.Join(path, i.Old), i) {
			return b, domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_item_changed", Message: "file changed; preview again", Details: map[string]any{"media": b.Media, "name": i.Old}})
		}
		if err = l.CheckAvailable(ctx, i.Entry); err != nil {
			return b, err
		}
		if e.TaskStatus != "" || (l.Busy != nil && l.Busy(ctx, i.Entry)) {
			return b, domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_item_busy", Message: "a file has an active job", Details: map[string]any{"media": b.Media}})
		}
		if i.UndoOf != 0 && e.RestoreID != i.UndoOf {
			return b, domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_item_renamed", Message: "a file was renamed later; the batch cannot be restored", Details: map[string]any{"media": b.Media}})
		}
	}
	if _, err = storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE image_numberings SET status='running' WHERE id=?", id)
	}); err != nil {
		return b, err
	}
	b.Status = "running"
	if err = l.performNumbering(ctx, &b, path, progress); err != nil {
		// Cancellation is one of the failures recovered here; the rollback and
		// its records must finish after ctx ends.
		ctx, cancel := storage.Detach(ctx)
		defer cancel()
		// A failed COMMIT can have an uncertain outcome. Inspect durable state
		// before moving files back; if the database is unavailable, let startup recover.
		persisted, readErr := l.Numbering(ctx, id)
		if readErr != nil {
			l.broken = readErr
			return b, domain.Fail(domain.KindInternal, domain.Record{Code: "numbering_unconfirmed", Message: "numbering result needs review; restart to recover"}).Wrap(readErr)
		}
		if persisted.Status == "committed" {
			return persisted, nil
		}
		b = persisted
		cause := err
		if diagnostic, ok := errors.AsType[*domain.JobError](cause); ok {
			b.ProcessingLog = diagnostic.ProcessingLog
		}
		b.Error = domain.RecordOf(cause)
		// Save the captured failure before rollback changes the filesystem.
		if _, saveErr := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "UPDATE image_numberings SET error=? WHERE id=?", b.numberingErrorRecord(), id)
		}); saveErr != nil {
			l.broken = saveErr
			return b, domain.Fail(domain.KindInternal, domain.Record{Code: "numbering_evidence_unsaved", Message: "saving the numbering failure failed; restart to review"}).Wrap(saveErr)
		}
		if recovery := l.rollbackNumbering(ctx, &b, path); recovery != nil {
			b.Status = "review"
			b.Error = domain.Record{Code: "numbering_recovery_incomplete", Message: fmt.Sprintf("%v; recovery incomplete: %v", cause, recovery), Details: map[string]any{"media": b.Media, "cause": domain.RecordOf(cause), "recovery": domain.RecordOf(recovery)}}
			if diagnostic, ok := errors.AsType[*domain.JobError](recovery); ok {
				b.ProcessingLog += "\nrecovery failure:\n" + diagnostic.ProcessingLog
			}
		} else {
			b.Status = "rolled_back"
			b.Error = domain.Record{Code: "numbering_rolled_back", Message: "numbering failed; original names restored: " + cause.Error(), Details: map[string]any{"cause": domain.RecordOf(cause)}}
		}
		if _, dbErr := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "UPDATE image_numberings SET status=?,error=? WHERE id=?", b.Status, b.numberingErrorRecord(), id)
		}); dbErr != nil {
			l.broken = dbErr
		}
		return b, &domain.JobError{Code: domain.ErrorCode(b.Error.Code), Message: b.Error.Message, Details: b.Error.Details, ProcessingLog: b.ProcessingLog}
	}
	return l.Numbering(ctx, id)
}

// moveNumberingOptions groups the inputs for moveNumbering.
type moveNumberingOptions struct {
	Batch  *Numbering
	Item   *NumberingItem
	Path   string
	Target string
}

// Persist the intent before each no-replace move, then fsync before recording completion.
func (l *Library) moveNumbering(ctx context.Context, params moveNumberingOptions) error {
	b, i, path, target := params.Batch, params.Item, params.Path, params.Target

	if i.Current == target {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A started move records its intent and result even if ctx ends meanwhile.
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	if checked, err := l.Path(ctx, b.Parent); err != nil || checked != path {
		return errors.New("parent directory changed")
	}
	dir, err := l.openDirectory(ctx, b.Parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	snap, err := directorySnapshot(dir, i.Current)
	if err != nil || Identity(snap) != i.Identity || snap.Size != i.Size || stamp(snap) != i.ModifiedNS {
		return domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_item_changed", Message: "file identity or content changed", Details: map[string]any{"media": b.Media, "name": i.Current}})
	}
	l.indexEpoch.Add(1)
	if _, err := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE image_numbering_items SET pending_name=? WHERE batch=? AND entry=?", target, b.ID, i.Entry)
	}); err != nil {
		return err
	}
	i.Pending = target
	if err := rename.NoReplaceAt(int(dir.Fd()), i.Current, target); err != nil {
		return numberingMoveFailure(numberingMoveFailureOptions{Batch: b, Item: i, Path: path, Target: target, Cause: err})
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if _, err := l.Path(ctx, b.Parent); err != nil {
		return err
	}
	if _, err := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE image_numbering_items SET current_name=?,pending_name='' WHERE batch=? AND entry=?", target, b.ID, i.Entry)
	}); err != nil {
		return err
	}
	i.Current = target
	i.Pending = ""
	return nil
}
func syncNumberingDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (l *Library) performNumbering(ctx context.Context, b *Numbering, path string, progress func(int, int)) error {
	done := 0
	for idx := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		i := &b.Items[idx]
		if i.Old != i.New {
			if err := l.moveNumbering(ctx, moveNumberingOptions{Batch: b, Item: i, Path: path, Target: i.Temp}); err != nil {
				return err
			}
		}
	}
	for idx := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		i := &b.Items[idx]
		if i.Old != i.New {
			if err := l.moveNumbering(ctx, moveNumberingOptions{Batch: b, Item: i, Path: path, Target: i.New}); err != nil {
				return err
			}
			done++
			if progress != nil {
				progress(done, b.Changed)
			}
		}
	}
	return l.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		// Temporarily free database name uniqueness within the same transaction.
		for _, i := range b.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i.Old != i.New {
				if _, err = tx.ExecContext(ctx, "UPDATE entries SET name=? WHERE id=?", i.Temp, i.Entry); err != nil {
					return err
				}
			}
		}
		for _, i := range b.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i.Old == i.New {
				continue
			}
			source := "manual"
			if b.UndoOf != "" {
				source = "restore"
			}
			o := Operation{Entry: i.Entry, Parent: b.Parent, Old: i.Old, New: i.New, Identity: i.Identity, Size: i.Size, ModifiedNS: i.ModifiedNS, Kind: "file", Source: source, UndoOf: i.UndoOf}
			res, err := tx.ExecContext(ctx, `INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime_ns,kind,source,status,undo_of,request_key,created) VALUES(?,?,?,?,?,?,?,?,?,'prepared',?,?,?)`, o.Entry, o.Parent, o.Old, o.New, o.Identity, o.Size, o.ModifiedNS, o.Kind, o.Source, o.UndoOf, "numbering:"+b.ID+fmt.Sprint(i.Entry), storage.Now())
			if err != nil {
				return err
			}
			o.ID, err = res.LastInsertId()
			if err != nil {
				return fmt.Errorf("read numbering operation ID: %w", err)
			}
			// Validated directory and leaf names share the absolute library root.
			oldrel, _ := filepath.Rel(l.Root, filepath.Join(path, i.Old))
			newrel, _ := filepath.Rel(l.Root, filepath.Join(path, i.New))
			if err = l.finishTx(ctx, finishTxOptions{Tx: tx, O: o, Oldrel: oldrel, Newrel: newrel}); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE image_numbering_items SET operation=? WHERE batch=? AND entry=?", o.ID, b.ID, i.Entry); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE image_numberings SET status='committed',error='' WHERE id=?", b.ID); err != nil {
			return err
		}
		if b.UndoOf != "" {
			if _, err = tx.ExecContext(ctx, "UPDATE image_numberings SET status='restored' WHERE id=?", b.UndoOf); err != nil {
				return err
			}
		}
		// Storage writes may wait while external processes replace the files.
		// Reject that outcome before committing any success record, including
		// batches whose numbering leaves some of the original names unchanged.
		return l.verifyNumberingCommit(ctx, b)
	})
}

func (l *Library) verifyNumberingCommit(ctx context.Context, b *Numbering) (err error) {
	dir, err := l.openDirectory(ctx, b.Parent)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	for _, item := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		operation := Operation{Kind: "file", Identity: item.Identity, Size: item.Size, ModifiedNS: item.ModifiedNS}
		if err := verifyOperationFile(dir, item.New, operation); err != nil {
			return domain.Fail(domain.KindConflict, domain.Record{Code: "numbering_item_changed", Message: "file changed before commit", Details: map[string]any{"media": b.Media, "name": item.New}}).Wrap(err)
		}
	}
	_, err = l.Path(ctx, b.Parent)
	return err
}
