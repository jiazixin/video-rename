package library

import (
	"context"
	"database/sql"

	"video-rename/internal/domain"
)

// pendingOperations belongs to one read snapshot. Callers must not reuse it
// after releasing Library.mu or across external callbacks.
type pendingOperations struct {
	numbering bool
	rename    bool
}

func readPendingOperations(ctx context.Context, tx *sql.Tx) (pending pendingOperations, err error) {
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM image_numberings WHERE status IN ('running','rollback','review')),
 EXISTS(SELECT 1 FROM operations WHERE status IN ('prepared','review'))`).Scan(&pending.numbering, &pending.rename)
	return pending, err
}

func (p pendingOperations) check(ctx context.Context, tx *sql.Tx, id int64) error {
	if p.numbering {
		if err := checkPendingNumbering(ctx, tx, id); err != nil {
			return err
		}
	}
	if !p.rename {
		return nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (
 SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1
 ), ancestors(id,parent) AS (
 SELECT id,parent FROM entries WHERE id=?
 UNION ALL SELECT e.id,e.parent FROM entries e JOIN ancestors a ON e.id=a.parent
 ) SELECT count(*) FROM operations WHERE status IN ('prepared','review')
 AND (entry IN (SELECT id FROM descendants) OR entry IN (SELECT id FROM ancestors))`, id, id).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return errPendingReview
	}
	return nil
}

func checkPendingNumbering(ctx context.Context, tx *sql.Tx, id int64) error {
	var n int
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (
 SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1
 ), ancestors(id,parent) AS (
 SELECT id,parent FROM entries WHERE id=?
 UNION ALL SELECT e.id,e.parent FROM entries e JOIN ancestors a ON e.id=a.parent
 ) SELECT count(*) FROM image_numberings WHERE status IN ('running','rollback','review')
 AND (parent IN (SELECT id FROM descendants) OR parent IN (SELECT id FROM ancestors))`, id, id).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingPending, Message: "directory has unfinished numbering; recover it from the numbering records"})
	}
	return nil
}
