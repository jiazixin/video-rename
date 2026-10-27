package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"video-rename/internal/domain"
	"video-rename/internal/naming"
	"video-rename/internal/rename"
	"video-rename/internal/storage"
)

// Operation is a recorded rename or restore of one entry.
type Operation struct {
	RawRequest  string        `json:"-"`
	RawResponse string        `json:"-"`
	ID          int64         `json:"id"`
	Entry       int64         `json:"entry"`
	Parent      int64         `json:"parent"`
	Old         string        `json:"old_name"`
	New         string        `json:"new_name"`
	Identity    string        `json:"-"`
	Size        int64         `json:"-"`
	Mtime       string        `json:"-"`
	Kind        string        `json:"kind"`
	Source      string        `json:"source"`
	Status      string        `json:"status"`
	UndoOf      int64         `json:"undo_of"`
	Undone      bool          `json:"undone"`
	Created     string        `json:"created"`
	Error       domain.Record `json:"error,omitzero"`
	Run         int64         `json:"run"`
}

const operationColumns = "id,entry,parent,old_name,new_name,identity,size,mtime,kind,source,status,undo_of,undone,created,error,run,raw_request,raw_response"

func scanOp(row interface{ Scan(...any) error }) (o Operation, err error) {
	err = row.Scan(&o.ID, &o.Entry, &o.Parent, &o.Old, &o.New, &o.Identity, &o.Size, &o.Mtime, &o.Kind, &o.Source, &o.Status, &o.UndoOf, &o.Undone, &o.Created, &o.Error, &o.Run, &o.RawRequest, &o.RawResponse)
	return
}

// Operation returns operation id.
func (l *Library) Operation(id int64) (Operation, error) {
	return scanOp(l.DB.Read().QueryRow("SELECT "+operationColumns+" FROM operations WHERE id=?", id))
}

// History returns the operations of entry id older than before.
func (l *Library) History(ctx context.Context, id int64, before int64) ([]Operation, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := l.DB.Read().QueryContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE entry=? AND id<? ORDER BY id DESC LIMIT 100", id, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		o, e := scanOp(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ValidName reports whether name is a valid file name.
func ValidName(name string) error {
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") || !utf8.ValidString(name) || len(name) > 240 {
		return domain.InvalidField("name", "invalid_name")
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune("/\\:*?\"<>|", r) {
			return domain.InvalidField("name", "invalid_character")
		}
	}
	return nil
}

// Rename renames entry id, at version, to name; key makes the request
// idempotent.
func (l *Library) Rename(ctx context.Context, id, version int64, name, key string) (o Operation, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, err := l.Get(ctx, id)
	if err != nil {
		return o, err
	}
	if e.Kind == "file" {
		ext := filepath.Ext(name)
		name = strings.TrimSuffix(name, ext) + naming.NormalizeExtension(ext)
	}
	if existing, er := scanOp(l.DB.QueryRow("SELECT "+operationColumns+" FROM operations WHERE request_key=?", key)); er == nil {
		if existing.Entry == id && existing.New == name && existing.Status == "committed" {
			return existing, nil
		}
		return o, errOperationKey
	}
	if er := l.CheckAvailable(id); er != nil {
		return o, er
	}
	if l.Busy != nil && l.Busy(id) {
		return o, errBusy
	}
	if e.Version != version {
		return o, errChanged
	}
	return l.commit(e, name, "manual", key, 0, 0)
}

// Restore undoes operation id; key makes the request idempotent.
func (l *Library) Restore(ctx context.Context, id int64, key string) (Operation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, er := scanOp(l.DB.QueryRow("SELECT "+operationColumns+" FROM operations WHERE request_key=?", key)); er == nil {
		if existing.UndoOf == id && existing.Status == "committed" {
			return existing, nil
		}
		return Operation{}, errOperationKey
	}
	old, err := l.Operation(id)
	if err != nil {
		return Operation{}, err
	}
	if l.Busy != nil && l.Busy(old.Entry) {
		return Operation{}, errBusy
	}
	if er := l.CheckAvailable(old.Entry); er != nil {
		return Operation{}, er
	}
	e, err := l.Get(ctx, old.Entry)
	if err != nil {
		return Operation{}, err
	}
	if e.RestoreID != id || e.Name != old.New {
		return Operation{}, domain.Fail(domain.KindConflict, "restore_not_latest", "only the latest rename that is not undone can be restored")
	}
	p, err := l.Path(e.ID)
	if err != nil {
		return Operation{}, err
	}
	s, err := domain.StatSnapshot(p)
	if err != nil {
		return Operation{}, err
	}
	if Identity(s) != old.Identity || (e.Kind != "directory" && (s.Size != old.Size || stamp(s) != old.Mtime)) {
		return Operation{}, domain.Fail(domain.KindConflict, "restore_source_changed", "file identity or content changed; cannot restore")
	}
	return l.commit(e, old.Old, "restore", key, id, 0)
}

// AutoCommit renames entry id to the model title after checking that it still
// matches snap.
func (l *Library) AutoCommit(ctx context.Context, id int64, snap domain.Snapshot, title, key string, run int64, diagnostics ...domain.ModelDiagnostics) (Operation, error) {
	return l.autoCommit(ctx, id, snap, title, key, run, 0, diagnostics...)
}

// RetryAutoCommit permits one explicit retry of the current automatic rename.
// The existing mark stays intact until the new rename commits successfully.
func (l *Library) RetryAutoCommit(ctx context.Context, id int64, snap domain.Snapshot, title, key string, run, operation int64, diagnostics ...domain.ModelDiagnostics) (Operation, error) {
	if operation <= 0 {
		return Operation{}, domain.InvalidField("operation", "invalid")
	}
	return l.autoCommit(ctx, id, snap, title, key, run, operation, diagnostics...)
}

// CheckAutomaticRetry checks that entry id can still be renamed automatically
// by retrying operation.
func (l *Library) CheckAutomaticRetry(ctx context.Context, id, operation int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, err := l.Get(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Fail(domain.KindNotFound, "entry_not_indexed", "file is no longer indexed")
		}
		return err
	}
	return l.checkAutomaticRetry(e, operation)
}

func (l *Library) checkAutomaticRetry(e Entry, operation int64) error {
	if operation <= 0 {
		return domain.Fail(domain.KindConflict, "retry_operation_missing", "record has no automatic rename to retry")
	}
	o, err := l.Operation(operation)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Fail(domain.KindConflict, "retry_operation_missing", "automatic rename operation does not exist")
		}
		return err
	}
	if e.Kind != "file" || e.Processed != "automatic" || e.RestoreID != operation ||
		o.Entry != e.ID || o.Source != "automatic" || o.Status != "committed" || o.Undone ||
		o.New != e.Name || o.Identity != e.Identity || o.Size != e.Size || o.Mtime != e.Mtime {
		return domain.Fail(domain.KindConflict, "retry_operation_stale", "only the current automatic rename can be retried")
	}
	return l.CheckAvailable(e.ID)
}

func (l *Library) autoCommit(ctx context.Context, id int64, snap domain.Snapshot, title, key string, run, retryOperation int64, diagnostics ...domain.ModelDiagnostics) (Operation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, err := l.Get(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	if retryOperation > 0 {
		if err := l.checkAutomaticRetry(e, retryOperation); err != nil {
			return Operation{}, err
		}
	} else if e.Processed != "" {
		return Operation{}, domain.Fail(domain.KindConflict, "already_processed", "file is already processed")
	}
	p, err := l.Path(id)
	if err != nil {
		return Operation{}, err
	}
	cur, err := domain.StatSnapshot(p)
	if err != nil {
		return Operation{}, domain.Fail(domain.KindConflict, "source_unreadable", "source file cannot be read after analysis").Wrap(err)
	}
	if !cur.Same(snap) {
		return Operation{}, domain.SourceChangedError("source file changed during analysis", domain.StageBeforeRename, p, snap, cur)
	}
	clean, je := naming.CleanTitle(title, filepath.Ext(e.Name))
	if je != nil {
		return Operation{}, je
	}
	alloc := naming.NewAllocator()
	for i := range 9999 {
		res, je := alloc.Allocate(filepath.Dir(p), clean, filepath.Ext(e.Name), domain.VideoJob{Path: p, Snapshot: snap})
		if je != nil {
			return Operation{}, je
		}
		o, err := l.commit(e, filepath.Base(res.Target), "automatic", fmt.Sprintf("%s:%d", key, i), 0, run, diagnostics...)
		if re, ok := errors.AsType[*domain.JobError](err); ok && re.Code == domain.CodeRenameConflict {
			alloc.Release(filepath.Dir(p), filepath.Base(res.Target))
			continue
		}
		return o, err
	}
	return Operation{}, domain.Fail(domain.KindConflict, "too_many_conflicts", "too many name conflicts")
}
func (l *Library) commit(e Entry, name, source, key string, undo, run int64, diagnostics ...domain.ModelDiagnostics) (Operation, error) {
	if err := l.checkNumberingAvailable(e.ID); err != nil {
		return Operation{}, err
	}
	if l.broken != nil {
		return Operation{}, errHalted.Wrap(l.broken)
	}
	if key == "" || len(key) > 200 {
		return Operation{}, domain.InvalidField("key", "invalid")
	}
	existing, er := scanOp(l.DB.QueryRow("SELECT "+operationColumns+" FROM operations WHERE request_key=?", key))
	if er == nil {
		if existing.Entry != e.ID || existing.New != name {
			return Operation{}, errOperationKey
		}
		if existing.Status != "committed" {
			return existing, domain.Fail(domain.KindConflict, "operation_not_committed", "previous operation is not committed", "status", existing.Status)
		}
		return existing, nil
	}
	if !errors.Is(er, sql.ErrNoRows) {
		return Operation{}, er
	}
	if e.ID == 1 || e.Kind == "symlink" || e.Kind == "other" {
		return Operation{}, errProtectedEntry
	}
	if err := ValidName(name); err != nil {
		return Operation{}, err
	}
	var blocked int
	if err := l.DB.QueryRow("SELECT count(*) FROM operations WHERE status IN ('prepared','review') AND entry=?", e.ID).Scan(&blocked); err != nil {
		return Operation{}, err
	}
	if blocked > 0 {
		return Operation{}, errPendingReview
	}
	p, err := l.Path(e.ID)
	if err != nil {
		return Operation{}, err
	}
	dir, err := l.openDirectory(e.Parent)
	if err != nil {
		return Operation{}, err
	}
	defer dir.Close()
	snap, err := directorySnapshot(dir, e.Name)
	if err != nil {
		return Operation{}, err
	}
	if Identity(snap) != e.Identity || (e.Kind != "directory" && (snap.Size != e.Size || stamp(snap) != e.Mtime)) {
		return Operation{}, errChanged
	}
	if name == e.Name && source == "manual" {
		return Operation{Entry: e.ID, Old: name, New: name, Status: "unchanged"}, nil
	}
	// Invalidate scans before journaling or moving, including uncertain outcomes.
	l.indexEpoch.Add(1)
	target := filepath.Join(filepath.Dir(p), name)
	oldrel, _ := filepath.Rel(l.Root, p)
	newrel, _ := filepath.Rel(l.Root, target)
	var exchange domain.ModelDiagnostics
	if len(diagnostics) > 0 {
		exchange = diagnostics[0]
	}
	res, err := l.DB.Exec("INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime,kind,source,status,undo_of,request_key,created,run,raw_request,raw_response) VALUES(?,?,?,?,?,?,?,?,?,'prepared',?,?,?,?,?,?)", e.ID, e.Parent, e.Name, name, e.Identity, snap.Size, stamp(snap), e.Kind, source, undo, key, storage.Now(), run, exchange.Request, exchange.Response)
	if err != nil {
		return Operation{}, err
	}
	oid, _ := res.LastInsertId()
	o, err := l.Operation(oid)
	if err != nil {
		l.broken = err
		return o, err
	}
	// Journaling can block long enough for another process to replace the leaf.
	// Retain prepared evidence on any mismatch; recovery must inspect both names.
	if err = verifyOperationFile(dir, e.Name, o); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, "journal_source_changed", "source changed after journaling; restart to review").Wrap(err)
	}
	if name != e.Name {
		if je := rename.NoReplaceAt(int(dir.Fd()), e.Name, name); je != nil {
			_, dbErr := l.DB.Exec("UPDATE operations SET status='failed',error=? WHERE id=?", domain.RecordText(je), oid)
			if dbErr != nil {
				l.broken = dbErr
			}

			return o, je
		}
		de := dir.Sync()
		if de != nil {
			l.broken = de
			return o, domain.Fail(domain.KindInternal, "rename_sync_failed", "renamed, but syncing the directory failed; restart to review").Wrap(de)
		}
	}
	if _, err = l.Path(e.Parent); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, "rename_parent_changed", "renamed, but the parent directory changed; restart to review").Wrap(err)
	}
	if err = verifyOperationFile(dir, name, o); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, "rename_result_changed", "renamed file identity or content changed; restart to review").Wrap(err)
	}
	if err = l.finish(o, oldrel, newrel); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, "rename_unconfirmed", "file may be renamed but the result is unconfirmed; restart to recover").Wrap(err)
	}
	o.Status = "committed"
	return o, nil
}

func verifyOperationFile(dir *os.File, name string, o Operation) error {
	snap, err := directorySnapshot(dir, name)
	if err != nil {
		return err
	}
	if Identity(snap) != o.Identity || (o.Kind != "directory" && (snap.Size != o.Size || stamp(snap) != o.Mtime)) {
		return errors.New("file identity or content does not match the operation journal")
	}
	return nil
}
func (l *Library) finish(o Operation, oldrel, newrel string) error {
	tx, err := l.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = l.finishTx(tx, o, oldrel, newrel); err != nil {
		return err
	}
	// Revalidate after database writes as well: they can wait on storage while
	// another process changes the renamed leaf. Roll back the success journal.
	dir, err := l.openDirectory(o.Parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = verifyOperationFile(dir, o.New, o); err != nil {
		return err
	}
	if _, err = l.Path(o.Parent); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *Library) finishTx(tx *sql.Tx, o Operation, oldrel, newrel string) error {
	var err error
	if _, err = tx.Exec("UPDATE entries SET name=?,version=version+1,size=?,mtime=? WHERE id=?", o.New, o.Size, o.Mtime, o.Entry); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE operations SET status='committed' WHERE id=?", o.ID); err != nil {
		return err
	}
	if o.UndoOf != 0 {
		if _, err = tx.Exec("UPDATE operations SET undone=1 WHERE id=?", o.UndoOf); err != nil {
			return err
		}
	}
	if o.Kind == "file" && o.Source == "restore" {
		if _, err = tx.Exec("DELETE FROM marks WHERE identity=? AND size=? AND mtime=?", o.Identity, o.Size, o.Mtime); err != nil {
			return err
		}
	} else if o.Kind == "file" {
		keepMark := IsImage(o.New) || IsAudio(o.New)
		if _, err = tx.Exec("INSERT INTO marks VALUES(?,?,?,?,?) ON CONFLICT(identity,size,mtime) DO UPDATE SET source=CASE WHEN marks.source='marked' AND ? THEN marks.source ELSE excluded.source END,operation=CASE WHEN marks.source='marked' AND ? THEN marks.operation ELSE excluded.operation END", o.Identity, o.Size, o.Mtime, o.Source, o.ID, keepMark, keepMark); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO events(category,action,entry,operation,run,old_path,new_path,created,raw_request,raw_response) VALUES('success',?,?,?,?,?,?,?,?,?)", o.Source, o.Entry, o.ID, o.Run, oldrel, newrel, storage.Now(), o.RawRequest, o.RawResponse); err != nil {
		return err
	}
	// A committed automatic rename resolves earlier naming failures for this entry.
	if o.Source == "automatic" {
		if _, err = tx.Exec("DELETE FROM events WHERE category='failure' AND action='automatic' AND entry=?", o.Entry); err != nil {
			return err
		}
	}
	// Preserve the generation and cursor anchors while updating the sorted index.
	// Clients displaying an existing page patch entries by ID without reordering.
	if _, err = tx.Exec("UPDATE directory_items SET name=? WHERE dir=? AND entry=?", o.New, o.Parent, o.Entry); err != nil {
		return err
	}
	return nil
}

// Event records a library event.
func (l *Library) Event(category, action string, entry, operation, run int64, old, new, message string) error {
	_, err := l.DB.Exec("INSERT INTO events(category,action,entry,operation,run,old_path,new_path,message,created) VALUES(?,?,?,?,?,?,?,?,?)", category, action, entry, operation, run, old, new, message, storage.Now())
	return err
}

// Recover resolves operations interrupted after preparation.
func (l *Library) Recover() error {
	rows, err := l.DB.Query("SELECT " + operationColumns + " FROM operations WHERE status='prepared'")
	if err != nil {
		return err
	}
	ops := []Operation{}
	for rows.Next() {
		o, er := scanOp(rows)
		if er != nil {
			rows.Close()
			return er
		}
		ops = append(ops, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range ops {
		parent, pe := l.Path(o.Parent)
		if pe != nil {
			if _, err = l.DB.Exec("UPDATE operations SET status='review',error=? WHERE id=?", domain.Record{Code: "parent_unverifiable", Message: "parent directory cannot be verified"}.Text(), o.ID); err != nil {
				return err
			}
			continue
		}
		old, new := filepath.Join(parent, o.Old), filepath.Join(parent, o.New)
		a, ae := domain.StatSnapshot(old)
		b, be := domain.StatSnapshot(new)
		match := func(s domain.Snapshot) bool {
			return Identity(s) == o.Identity && (o.Kind == "directory" || (s.Size == o.Size && stamp(s) == o.Mtime))
		}
		oldrel, _ := filepath.Rel(l.Root, old)
		newrel, _ := filepath.Rel(l.Root, new)
		if (errors.Is(ae, fs.ErrNotExist) || o.Old == o.New) && be == nil && match(b) {
			if err = l.finish(o, oldrel, newrel); err != nil {
				return err
			}
			continue
		}
		status, msg := "review", domain.Record{Code: "rename_evidence_mismatch", Message: "paths before and after the rename disagree; review needed"}.Text()
		if ae == nil && match(a) && errors.Is(be, fs.ErrNotExist) {
			status = "interrupted"
			msg = domain.Record{Code: "rename_not_started", Message: "rename did not happen; the run was interrupted"}.Text()
		}
		if _, err = l.DB.Exec("UPDATE operations SET status=?,error=? WHERE id=?", status, msg, o.ID); err != nil {
			return err
		}
		if err = l.Event("failure", "recovery", o.Entry, o.ID, o.Run, oldrel, newrel, msg); err != nil {
			return err
		}
	}
	return nil
}

// CheckAvailable blocks uncertain filesystem operations on the entry and related directories.
func (l *Library) CheckAvailable(id int64) error {
	if err := l.checkNumberingAvailable(id); err != nil {
		return err
	}
	var pending bool
	if err := l.DB.Read().QueryRow("SELECT EXISTS(SELECT 1 FROM operations WHERE status IN ('prepared','review'))").Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	var n int
	err := l.DB.Read().QueryRow(`WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1), ancestors(id,parent) AS (SELECT id,parent FROM entries WHERE id=? UNION ALL SELECT e.id,e.parent FROM entries e JOIN ancestors a ON e.id=a.parent) SELECT count(*) FROM operations WHERE status IN ('prepared','review') AND (entry IN (SELECT id FROM descendants) OR entry IN (SELECT id FROM ancestors))`, id, id).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return errPendingReview
	}
	return nil
}

// Halted reports whether the library stopped changing files after an
// unrecoverable failure.
func (l *Library) Halted() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.broken != nil }
