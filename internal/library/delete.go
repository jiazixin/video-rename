package library

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// Delete permanently removes an entry and its contents; rename history is retained.
func (l *Library) Delete(ctx context.Context, id, version int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.broken != nil {
		return errHalted.Wrap(l.broken)
	}
	e, err := l.Get(ctx, id)
	if err != nil {
		return err
	}
	if id == 1 || (e.Kind != "file" && e.Kind != "directory") {
		return errProtectedEntry
	}
	if e.Version != version {
		return errChanged
	}
	if err := l.CheckAvailable(ctx, id); err != nil {
		return err
	}
	if e.TaskStatus != "" || (l.Busy != nil && l.Busy(ctx, id)) {
		return errBusy
	}
	var active int
	if err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (
	 SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1
	) SELECT count(*) FROM runs r JOIN json_each(r.targets) t
	 WHERE r.status IN `+domain.ActiveRunStatusesSQL+` AND t.value IN (SELECT id FROM descendants)`, id).Scan(&active)
	}); err != nil {
		return err
	}
	if active > 0 {
		return domain.Fail(domain.KindConflict, domain.Record{Code: "descendant_busy", Message: "a descendant has an active job"})
	}
	p, err := l.Path(ctx, id)
	if err != nil {
		return err
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		return err
	}
	if Identity(snap) != e.Identity || (e.Kind == "file" && (snap.Size != e.Size || stamp(snap) != e.ModifiedNS)) {
		return errChanged
	}
	root, err := os.OpenRoot(l.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	rel, err := filepath.Rel(l.Root, p)
	if err != nil {
		return err
	}
	if _, err := l.Path(ctx, id); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Once files are removed, the index must follow even if the request ends.
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	l.indexEpoch.Add(1)
	if err := root.RemoveAll(rel); err != nil {
		return domain.Fail(domain.KindInternal, domain.Record{Code: "delete_incomplete", Message: "delete stopped; the directory may be partly deleted"}).Wrap(err)
	}
	_, err = storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (
	 SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id
	) UPDATE entries SET present=0,version=version+1 WHERE id IN (SELECT id FROM descendants)`, id)
	})
	if err != nil {
		l.broken = err
		return domain.Fail(domain.KindInternal, domain.Record{Code: "delete_index_failed", Message: "deleted, but the index update failed"}).Wrap(err)
	}
	if _, err := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "DELETE FROM directory_items WHERE entry IN (SELECT id FROM entries WHERE present=0)")
	}); err != nil {
		return domain.Fail(domain.KindInternal, domain.Record{Code: "delete_listing_failed", Message: "deleted, but the listing update failed"}).Wrap(err)
	}
	return nil
}
