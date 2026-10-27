package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// A file result and its counter are one durable fact. Never publish only half.
func (m *Manager) finishItem(ctx context.Context, run, id int64, status, message string) error {
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	column := ""
	switch status {
	case "done", "skipped", "failed":
		column = status
	case "cancelled":
	default:
		return fmt.Errorf("invalid item status %q", status)
	}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE run_items SET status=?,message=?,finished_ms=?
WHERE id=? AND run=? AND status IN ('queued','running')`, status, message, time.Now().UnixMilli(), id, run)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("item status changed")
	}
	if column != "" {
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET "+column+"="+column+"+1 WHERE id=?", run); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func cancelUnfinished(ctx context.Context, tx *sql.Tx, id int64, message string) error {
	_, err := tx.ExecContext(ctx, `UPDATE run_items SET status='cancelled',message=?,finished_ms=?
WHERE run=? AND status IN ('queued','running')`, message, time.Now().UnixMilli(), id)
	return err
}

// If persistence remains unavailable, leave the batch active for startup recovery.
// File operation journals remain authoritative even when item persistence failed.
func (m *Manager) failRun(ctx context.Context, id, target int64, cause error) error {
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = cancelUnfinished(ctx, tx, id, runFailedRecord(cause)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status='failed',failed=failed+CASE WHEN ?>0 THEN 1 ELSE 0 END,error=?,finished=? WHERE id=?`, target, domain.RecordText(cause), storage.Now(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) completeRun(ctx context.Context, id int64, cancelled bool) error {
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done, skipped, failed int
	var current, message string
	if err = tx.QueryRowContext(ctx, "SELECT status,done,skipped,failed,error FROM runs WHERE id=?", id).Scan(&current, &done, &skipped, &failed, &message); err != nil {
		return err
	}
	status := "completed"
	if cancelled || current == "cancelling" {
		status = "cancelled"
		if message != "" {
			status = "failed"
		}
		if err = cancelUnfinished(ctx, tx, id, recordCancelled); err != nil {
			return err
		}
	} else {
		var unfinished bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM run_items WHERE run=? AND status IN ('queued','running'))", id).Scan(&unfinished); err != nil {
			return err
		}
		if unfinished {
			return errors.New("run has unfinished items")
		}
		if failed > 0 {
			status = "failed"
			if done > 0 || skipped > 0 {
				status = "partial_failure"
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,finished=? WHERE id=?", status, storage.Now(), id); err != nil {
		return err
	}
	return tx.Commit()
}
