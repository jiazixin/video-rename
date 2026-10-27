package library

import (
	"context"
	"crypto/rand"
	"fmt"

	"video-rename/internal/domain"
)

// BatchRestoreResult reports the outcome of restoring several renames.
type BatchRestoreResult struct {
	Restored int                   `json:"restored"`
	Skipped  int                   `json:"skipped"`
	Failed   int                   `json:"failed"`
	Errors   []BatchRestoreFailure `json:"errors"`
}

// BatchRestoreFailure is one rename that could not be restored.
type BatchRestoreFailure struct {
	Operation int64         `json:"operation"`
	Name      string        `json:"name"`
	Error     domain.Record `json:"error"`
}

// RestoreBatch undoes only successful renames belonging to this finished run.
// Each operation retains the normal identity, conflict and latest-operation checks.
func (l *Library) RestoreBatch(ctx context.Context, run int64) (BatchRestoreResult, error) {
	out := BatchRestoreResult{Errors: []BatchRestoreFailure{}}
	var status string
	if err := l.DB.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", run).Scan(&status); err != nil {
		return out, err
	}
	if domain.ActiveRunStatus(status) {
		return out, domain.Fail(domain.KindConflict, "run_active", "cancel the run and wait for it to finish first")
	}
	rows, err := l.DB.QueryContext(ctx, "SELECT id FROM operations WHERE run=? AND source='automatic' AND status='committed' AND undo_of=0 ORDER BY id DESC", run)
	if err != nil {
		return out, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		op, err := l.Operation(ctx, id)
		if err == nil && op.Undone {
			out.Skipped++
			continue
		}
		if err == nil {
			_, err = l.Restore(ctx, id, fmt.Sprintf("batch-restore:%d:%d:%s", run, id, rand.Text()))
		}
		if err != nil {
			out.Failed++
			if len(out.Errors) < 100 {
				out.Errors = append(out.Errors, BatchRestoreFailure{Operation: id, Name: op.New, Error: domain.RecordOf(err)})
			}
			continue
		}
		out.Restored++
	}
	return out, nil
}
