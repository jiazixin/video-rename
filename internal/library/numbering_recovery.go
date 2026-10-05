package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"video-rename/internal/domain"
)

// Resolve a crash between filesystem rename and journal completion without guessing.
func (l *Library) reconcileNumbering(b *Numbering, path string) error {
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
				return fmt.Errorf("无法唯一确定%s %s 的位置，需要人工核对", mediaLabel(b.Media), i.Old)
			}
			if pending {
				i.Current = i.Pending
			}
			if _, err = l.DB.Exec("UPDATE image_numbering_items SET current_name=?,pending_name='' WHERE batch=? AND entry=?", i.Current, b.ID, i.Entry); err != nil {
				return err
			}
			i.Pending = ""
		} else if !current {
			return fmt.Errorf("%s %s 已被外部修改，需要人工核对", mediaLabel(b.Media), i.Old)
		}
	}
	return syncNumberingDir(path)
}
func (l *Library) rollbackNumbering(b *Numbering, path string) error {
	if _, err := l.DB.Exec("UPDATE image_numberings SET status='rollback' WHERE id=?", b.ID); err != nil {
		return err
	}
	b.Status = "rollback"
	if err := l.reconcileNumbering(b, path); err != nil {
		return err
	}
	// Stage first, including final names that may occupy another item's original name.
	for idx := range b.Items {
		i := &b.Items[idx]
		if i.Current != i.Old {
			if err := l.moveNumbering(b, i, path, i.Temp); err != nil {
				return err
			}
		}
	}
	for idx := range b.Items {
		i := &b.Items[idx]
		if i.Current != i.Old {
			if err := l.moveNumbering(b, i, path, i.Old); err != nil {
				return err
			}
		}
	}
	return nil
}
func (l *Library) recoverNumberings() error {
	rows, err := l.DB.Query("SELECT id FROM image_numberings WHERE status IN ('running','rollback','review') ORDER BY created")
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
		if _, err = l.recoverNumbering(id); err != nil {
			return err
		}
	}
	return nil
}
func (l *Library) RecoverNumbering(id string) (Numbering, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recoverNumbering(id)
}
func (l *Library) recoverNumbering(id string) (Numbering, error) {
	b, err := l.Numbering(id)
	if err != nil {
		return b, err
	}
	if b.Status != "running" && b.Status != "rollback" && b.Status != "review" {
		return b, nil
	}
	path, err := l.Path(b.Parent)
	if err == nil {
		err = l.rollbackNumbering(&b, path)
	}
	b.Status = "rolled_back"
	b.Error = fmt.Sprintf("中断的%s编号已恢复原名", mediaLabel(b.Media))
	if err != nil {
		b.Status = "review"
		b.Error = fmt.Sprintf("%s编号恢复未完成：%v", mediaLabel(b.Media), err)
		if diagnostic, ok := errors.AsType[*domain.JobError](err); ok {
			b.ProcessingLog += "\n恢复失败现场：\n" + diagnostic.ProcessingLog
		}
	}
	_, dbErr := l.DB.Exec("UPDATE image_numberings SET status=?,error=? WHERE id=?", b.Status, b.numberingErrorRecord(), id)
	return b, dbErr
}
