package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"video-rename/internal/domain"
	"video-rename/internal/rename"
	"video-rename/internal/storage"
)

func (l *Library) ApplyNumbering(id string) (Numbering, error) {
	return l.ApplyNumberingContext(context.Background(), id, nil)
}

func (l *Library) ApplyNumberingContext(ctx context.Context, id string, progress func(int, int)) (Numbering, error) {
	if err := l.lockNumbering(ctx); err != nil {
		return Numbering{}, err
	}
	defer l.mu.Unlock()
	b, err := l.Numbering(id)
	if err != nil {
		return b, err
	}
	if b.Status == "committed" || b.Status == "restored" {
		return b, nil
	}
	if b.Status != "preview" {
		return b, fmt.Errorf("该批次%s，请查看编号记录", b.Status)
	}
	if l.broken != nil {
		return b, fmt.Errorf("服务有待恢复操作，请重启核对")
	}
	if err = l.CheckAvailable(b.Parent); err != nil {
		return b, err
	}
	if l.Busy != nil && l.Busy(b.Parent) {
		return b, fmt.Errorf("目录有活动任务，请等待任务完成")
	}
	path, err := l.Path(b.Parent)
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
		original, err := l.Numbering(b.UndoOf)
		if err != nil {
			return b, err
		}
		if original.Status != "committed" {
			return b, fmt.Errorf("原批次已还原或不可用")
		}
	}
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		e, err := l.Get(i.Entry)
		if err != nil {
			return b, err
		}
		if b.UndoOf == "" && e.Processed == "marked" {
			return b, fmt.Errorf("%s %s 已标记为跳过编号，请重新预览", mediaLabel(b.Media), e.Name)
		}
		if e.Parent != b.Parent || e.Version != i.Version || e.Name != i.Old || !numberingMatch(filepath.Join(path, i.Old), i) {
			return b, fmt.Errorf("%s %s 已变化，请重新预览", mediaLabel(b.Media), i.Old)
		}
		if err = l.CheckAvailable(i.Entry); err != nil {
			return b, err
		}
		if e.TaskStatus != "" || (l.Busy != nil && l.Busy(i.Entry)) {
			return b, fmt.Errorf("%s有活动任务，请等待完成", mediaLabel(b.Media))
		}
		if i.UndoOf != 0 && e.RestoreID != i.UndoOf {
			return b, fmt.Errorf("%s有后续改名，不能整批还原", mediaLabel(b.Media))
		}
	}
	if _, err = l.DB.Exec("UPDATE image_numberings SET status='running' WHERE id=?", id); err != nil {
		return b, err
	}
	b.Status = "running"
	if err = l.performNumbering(ctx, &b, path, progress); err != nil {
		// A failed COMMIT can have an uncertain outcome. Inspect durable state
		// before moving files back; if the database is unavailable, let startup recover.
		persisted, readErr := l.Numbering(id)
		if readErr != nil {
			l.broken = readErr
			return b, fmt.Errorf("编号结果待核对，请重启恢复：%w", readErr)
		}
		if persisted.Status == "committed" {
			return persisted, nil
		}
		b = persisted
		cause := err
		if diagnostic, ok := errors.AsType[*domain.JobError](cause); ok {
			b.ProcessingLog = diagnostic.ProcessingLog
		}
		b.Error = cause.Error()
		// Save the captured failure before rollback changes the filesystem.
		if _, saveErr := l.DB.Exec("UPDATE image_numberings SET error=? WHERE id=?", b.numberingErrorRecord(), id); saveErr != nil {
			l.broken = saveErr
			return b, fmt.Errorf("保存编号失败现场失败，需重启核对：%w", saveErr)
		}
		if recovery := l.rollbackNumbering(&b, path); recovery != nil {
			b.Status = "review"
			b.Error = fmt.Sprintf("%v；恢复未完成：%v", cause, recovery)
			if diagnostic, ok := errors.AsType[*domain.JobError](recovery); ok {
				b.ProcessingLog += "\n恢复失败现场：\n" + diagnostic.ProcessingLog
			}
		} else {
			b.Status = "rolled_back"
			b.Error = fmt.Sprintf("操作失败，已恢复原名：%v", cause)
		}
		if _, dbErr := l.DB.Exec("UPDATE image_numberings SET status=?,error=? WHERE id=?", b.Status, b.numberingErrorRecord(), id); dbErr != nil {
			l.broken = dbErr
		}
		if diagnostic, ok := errors.AsType[*domain.JobError](cause); ok {
			return b, &domain.JobError{Code: diagnostic.Code, Message: b.Error, ProcessingLog: b.ProcessingLog}
		}
		return b, errors.New(b.Error)
	}
	return l.Numbering(id)
}

// Persist the intent before each no-replace move, then fsync before recording completion.
func (l *Library) moveNumbering(b *Numbering, i *NumberingItem, path, target string) error {
	if i.Current == target {
		return nil
	}
	if checked, err := l.Path(b.Parent); err != nil || checked != path {
		return fmt.Errorf("父目录已变化")
	}
	dir, err := l.openDirectory(b.Parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	snap, err := directorySnapshot(dir, i.Current)
	if err != nil || Identity(snap) != i.Identity || snap.Size != i.Size || stamp(snap) != i.Mtime {
		return fmt.Errorf("%s %s 身份或内容已变化", mediaLabel(b.Media), i.Current)
	}
	l.indexEpoch.Add(1)
	if _, err := l.DB.Exec("UPDATE image_numbering_items SET pending_name=? WHERE batch=? AND entry=?", target, b.ID, i.Entry); err != nil {
		return err
	}
	i.Pending = target
	if err := rename.NoReplaceAt(int(dir.Fd()), i.Current, target); err != nil {
		return numberingMoveFailure(b, i, path, target, err)
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if _, err := l.Path(b.Parent); err != nil {
		return err
	}
	if _, err := l.DB.Exec("UPDATE image_numbering_items SET current_name=?,pending_name='' WHERE batch=? AND entry=?", target, b.ID, i.Entry); err != nil {
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
			if err := l.moveNumbering(b, i, path, i.Temp); err != nil {
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
			if err := l.moveNumbering(b, i, path, i.New); err != nil {
				return err
			}
			done++
			if progress != nil {
				progress(done, b.Changed)
			}
		}
	}
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !numberingMatch(filepath.Join(path, i.New), i) {
			return fmt.Errorf("%s %s 在提交前发生变化", mediaLabel(b.Media), i.New)
		}
	}
	tx, err := l.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Temporarily free database name uniqueness within the same transaction.
	for _, i := range b.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i.Old != i.New {
			if _, err = tx.Exec("UPDATE entries SET name=? WHERE id=?", i.Temp, i.Entry); err != nil {
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
		o := Operation{Entry: i.Entry, Parent: b.Parent, Old: i.Old, New: i.New, Identity: i.Identity, Size: i.Size, Mtime: i.Mtime, Kind: "file", Source: source, UndoOf: i.UndoOf}
		res, err := tx.Exec(`INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime,kind,source,status,undo_of,request_key,created) VALUES(?,?,?,?,?,?,?,?,?,'prepared',?,?,?)`, o.Entry, o.Parent, o.Old, o.New, o.Identity, o.Size, o.Mtime, o.Kind, o.Source, o.UndoOf, "numbering:"+b.ID+fmt.Sprint(i.Entry), storage.Now())
		if err != nil {
			return err
		}
		o.ID, _ = res.LastInsertId()
		oldrel, _ := filepath.Rel(l.Root, filepath.Join(path, i.Old))
		newrel, _ := filepath.Rel(l.Root, filepath.Join(path, i.New))
		if err = l.finishTx(tx, o, oldrel, newrel); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE image_numbering_items SET operation=? WHERE batch=? AND entry=?", o.ID, b.ID, i.Entry); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE image_numberings SET status='committed',error='' WHERE id=?", b.ID); err != nil {
		return err
	}
	if b.UndoOf != "" {
		if _, err = tx.Exec("UPDATE image_numberings SET status='restored' WHERE id=?", b.UndoOf); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}
