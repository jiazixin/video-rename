package jobs

import (
	"database/sql"
	"fmt"
	"time"

	"video-rename/internal/storage"
)

// A file result and its counter are one durable fact. Never publish only half.
func (m *Manager) finishItem(run, id int64, status, message string) error {
	column := ""
	switch status {
	case "done", "skipped", "failed":
		column = status
	case "cancelled":
	default:
		return fmt.Errorf("无效文件终态 %q", status)
	}
	tx, err := m.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE run_items SET status=?,message=?,finished_ms=?
WHERE id=? AND run=? AND status IN ('queued','running')`, status, message, time.Now().UnixMilli(), id, run)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("文件处理状态已变化")
	}
	if column != "" {
		if _, err = tx.Exec("UPDATE runs SET "+column+"="+column+"+1 WHERE id=?", run); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func cancelUnfinished(tx *sql.Tx, id int64, message string) error {
	_, err := tx.Exec(`UPDATE run_items SET status='cancelled',message=?,finished_ms=?
WHERE run=? AND status IN ('queued','running')`, message, time.Now().UnixMilli(), id)
	return err
}

// If persistence remains unavailable, leave the batch active for startup recovery.
// File operation journals remain authoritative even when item persistence failed.
func (m *Manager) failRun(id, target int64, cause error) error {
	tx, err := m.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = cancelUnfinished(tx, id, "批次失败，处理已停止："+cause.Error()); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE runs SET status='failed',failed=failed+CASE WHEN ?>0 THEN 1 ELSE 0 END,error=?,finished=? WHERE id=?`, target, cause.Error(), storage.Now(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) completeRun(id int64, cancelled bool) error {
	tx, err := m.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done, skipped, failed int
	var current, message string
	if err = tx.QueryRow("SELECT status,done,skipped,failed,error FROM runs WHERE id=?", id).Scan(&current, &done, &skipped, &failed, &message); err != nil {
		return err
	}
	status := "completed"
	if cancelled || current == "cancelling" {
		status = "cancelled"
		if message != "" {
			status = "failed"
		}
		if err = cancelUnfinished(tx, id, "已取消"); err != nil {
			return err
		}
	} else {
		var unfinished bool
		if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM run_items WHERE run=? AND status IN ('queued','running'))", id).Scan(&unfinished); err != nil {
			return err
		}
		if unfinished {
			return fmt.Errorf("仍有未结束文件，不能完成批次")
		}
		if failed > 0 {
			status = "failed"
			if done > 0 || skipped > 0 {
				status = "partial_failure"
			}
		}
	}
	if _, err = tx.Exec("UPDATE runs SET status=?,finished=? WHERE id=?", status, storage.Now(), id); err != nil {
		return err
	}
	return tx.Commit()
}
