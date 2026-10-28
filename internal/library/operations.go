package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
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
	ModifiedNS  int64         `json:"-"`
	Kind        string        `json:"kind"`
	Source      string        `json:"source"`
	Status      string        `json:"status"`
	UndoOf      int64         `json:"undo_of"`
	Undone      bool          `json:"undone"`
	Created     string        `json:"created"`
	Error       domain.Record `json:"error,omitzero"`
	Run         int64         `json:"run"`
}

const operationColumns = "id,entry,parent,old_name,new_name,identity,size,mtime_ns,kind,source,status,undo_of,undone,created,error,run,raw_request,raw_response"

func scanOp(row interface{ Scan(...any) error }) (o Operation, err error) {
	err = row.Scan(&o.ID, &o.Entry, &o.Parent, &o.Old, &o.New, &o.Identity, &o.Size, &o.ModifiedNS, &o.Kind, &o.Source, &o.Status, &o.UndoOf, &o.Undone, &o.Created, &o.Error, &o.Run, &o.RawRequest, &o.RawResponse)
	return
}

// Operation returns operation id.
func (l *Library) Operation(ctx context.Context, id int64) (Operation, error) {
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) (Operation, error) {
		return scanOp(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE id=?", id))
	})
}

// OperationPage is a stable descending cursor page of one entry's operations.
type OperationPage struct {
	Items      []Operation `json:"items"`
	NextCursor string      `json:"next_cursor,omitzero"`
}

const operationPageSize = 100

// History returns operations older than the exclusive cursor, preserving append-only ordering.
func (l *Library) History(ctx context.Context, id int64, cursor string) (OperationPage, error) {
	bound := int64(math.MaxInt64)
	if cursor != "" {
		var err error
		bound, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || bound <= 0 {
			return OperationPage{}, domain.InvalidField("cursor", "invalid")
		}
	}
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) (OperationPage, error) {
		rows, err := tx.QueryContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE entry=? AND id<? ORDER BY id DESC LIMIT ?", id, bound, operationPageSize+1)
		if err != nil {
			return OperationPage{}, err
		}
		items, err := storage.CollectRows(rows, func(rows *sql.Rows) (Operation, error) { return scanOp(rows) })
		if err != nil {
			return OperationPage{}, err
		}
		page := OperationPage{Items: items}
		if len(items) > operationPageSize {
			page.Items = items[:operationPageSize]
			page.NextCursor = strconv.FormatInt(page.Items[len(page.Items)-1].ID, 10)
		}
		return page, nil
	})
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

// RenameRequest groups the inputs for Rename.
type RenameRequest struct {
	ID      int64
	Version int64
	Name    string
	Key     string
}

// Rename renames entry id, at version, to name; key makes the request
// idempotent.
func (l *Library) Rename(ctx context.Context, params RenameRequest) (o Operation, err error) {
	id, version, name, key := params.ID, params.Version, params.Name, params.Key

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
	if existing, er := l.operationByKey(ctx, key); er == nil {
		if existing.Entry == id && existing.New == name && existing.Status == "committed" {
			return existing, nil
		}
		return o, errOperationKey
	}
	if er := l.CheckAvailable(ctx, id); er != nil {
		return o, er
	}
	if l.Busy != nil && l.Busy(ctx, id) {
		return o, errBusy
	}
	if e.Version != version {
		return o, errChanged
	}
	return l.commit(ctx, commitOptions{Entry: e, Name: name, Source: "manual", Key: key, Undo: 0, Run: 0})
}

// Restore undoes operation id; key makes the request idempotent.
func (l *Library) Restore(ctx context.Context, id int64, key string) (Operation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, er := l.operationByKey(ctx, key); er == nil {
		if existing.UndoOf == id && existing.Status == "committed" {
			return existing, nil
		}
		return Operation{}, errOperationKey
	}
	old, err := l.Operation(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	if l.Busy != nil && l.Busy(ctx, old.Entry) {
		return Operation{}, errBusy
	}
	if er := l.CheckAvailable(ctx, old.Entry); er != nil {
		return Operation{}, er
	}
	e, err := l.Get(ctx, old.Entry)
	if err != nil {
		return Operation{}, err
	}
	if e.RestoreID != id || e.Name != old.New {
		return Operation{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonRestoreNotLatest, Message: "only the latest rename that is not undone can be restored"})
	}
	p, err := l.Path(ctx, e.ID)
	if err != nil {
		return Operation{}, err
	}
	s, err := domain.StatSnapshot(p)
	if err != nil {
		return Operation{}, err
	}
	if Identity(s) != old.Identity || (e.Kind != "directory" && (s.Size != old.Size || stamp(s) != old.ModifiedNS)) {
		return Operation{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonRestoreSourceChanged, Message: "file identity or content changed; cannot restore"})
	}
	return l.commit(ctx, commitOptions{Entry: e, Name: old.Old, Source: "restore", Key: key, Undo: id, Run: 0})
}

// AutoCommitOptions groups the inputs for AutoCommit.
type AutoCommitOptions struct {
	ID          int64
	Snap        domain.Snapshot
	Title       string
	Key         string
	Run         int64
	Diagnostics []domain.ModelDiagnostics
}

// AutoCommit renames entry id to the model title after checking that it still
// matches snap.
func (l *Library) AutoCommit(ctx context.Context, params AutoCommitOptions) (Operation, error) {
	id, snap, title, key, run, diagnostics := params.ID, params.Snap, params.Title, params.Key, params.Run, params.Diagnostics

	return l.autoCommit(ctx, autoCommitOptions{ID: id, Snap: snap, Title: title, Key: key, Run: run, RetryOperation: 0, Diagnostics: diagnostics})
}

// RetryAutoCommitOptions groups the inputs for RetryAutoCommit.
type RetryAutoCommitOptions struct {
	ID          int64
	Snap        domain.Snapshot
	Title       string
	Key         string
	Run         int64
	Operation   int64
	Diagnostics []domain.ModelDiagnostics
}

// RetryAutoCommit permits one explicit retry of the current automatic rename.
// The existing mark stays intact until the new rename commits successfully.
func (l *Library) RetryAutoCommit(ctx context.Context, params RetryAutoCommitOptions) (Operation, error) {
	id, snap, title, key, run, operation, diagnostics := params.ID, params.Snap, params.Title, params.Key, params.Run, params.Operation, params.Diagnostics

	if operation <= 0 {
		return Operation{}, domain.InvalidField("operation", "invalid")
	}
	return l.autoCommit(ctx, autoCommitOptions{ID: id, Snap: snap, Title: title, Key: key, Run: run, RetryOperation: operation, Diagnostics: diagnostics})
}

// CheckAutomaticRetry checks that entry id can still be renamed automatically
// by retrying operation.
func (l *Library) CheckAutomaticRetry(ctx context.Context, id, operation int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, err := l.Get(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Fail(domain.KindNotFound, domain.Failure{Reason: domain.ReasonEntryNotIndexed, Message: "file is no longer indexed"})
		}
		return err
	}
	return l.checkAutomaticRetry(ctx, e, operation)
}

func (l *Library) checkAutomaticRetry(ctx context.Context, e Entry, operation int64) error {
	if operation <= 0 {
		return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonRetryOperationMissing, Message: "record has no automatic rename to retry"})
	}
	o, err := l.Operation(ctx, operation)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonRetryOperationMissing, Message: "automatic rename operation does not exist"})
		}
		return err
	}
	if e.Kind != "file" || e.Processed != "automatic" || e.RestoreID != operation ||
		o.Entry != e.ID || o.Source != "automatic" || o.Status != "committed" || o.Undone ||
		o.New != e.Name || o.Identity != e.Identity || o.Size != e.Size || o.ModifiedNS != e.ModifiedNS {
		return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonRetryOperationStale, Message: "only the current automatic rename can be retried"})
	}
	return l.CheckAvailable(ctx, e.ID)
}

// autoCommitOptions groups the inputs for autoCommit.
type autoCommitOptions struct {
	ID             int64
	Snap           domain.Snapshot
	Title          string
	Key            string
	Run            int64
	RetryOperation int64
	Diagnostics    []domain.ModelDiagnostics
}

func (l *Library) autoCommit(ctx context.Context, params autoCommitOptions) (Operation, error) {
	id, snap, title, key, run, retryOperation, diagnostics := params.ID, params.Snap, params.Title, params.Key, params.Run, params.RetryOperation, params.Diagnostics

	l.mu.Lock()
	defer l.mu.Unlock()
	e, err := l.Get(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	if retryOperation > 0 {
		if err := l.checkAutomaticRetry(ctx, e, retryOperation); err != nil {
			return Operation{}, err
		}
	} else if e.Processed != "" {
		return Operation{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonAlreadyProcessed, Message: "file is already processed"})
	}
	p, err := l.Path(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	cur, err := domain.StatSnapshot(p)
	if err != nil {
		return Operation{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonSourceUnreadable, Message: "source file cannot be read after analysis"}).Wrap(err)
	}
	if !cur.Same(snap) {
		return Operation{}, domain.SourceChangedError(domain.SourceChange{Message: "source file changed during analysis", Stage: domain.StageBeforeRename, Path: p, Before: snap, After: cur})
	}
	clean, je := naming.CleanTitle(title, filepath.Ext(e.Name))
	if je != nil {
		return Operation{}, je
	}
	alloc := naming.NewAllocator()
	for i := range 9999 {
		res, je := alloc.Allocate(ctx, naming.Allocation{Directory: filepath.Dir(p), Title: clean, Extension: filepath.Ext(e.Name), Job: domain.VideoJob{Path: p, Snapshot: snap}})
		if je != nil {
			return Operation{}, je
		}
		o, err := l.commit(ctx, commitOptions{Entry: e, Name: filepath.Base(res.Target), Source: "automatic", Key: fmt.Sprintf("%s:%d", key, i), Undo: 0, Run: run, Diagnostics: diagnostics})
		if re, ok := errors.AsType[*domain.JobError](err); ok && re.Code == domain.CodeRenameConflict {
			alloc.Release(filepath.Dir(p), filepath.Base(res.Target))
			continue
		}
		return o, err
	}
	return Operation{}, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonTooManyConflicts, Message: "too many name conflicts"})
}

// commitOptions groups the inputs for commit.
type commitOptions struct {
	Entry       Entry
	Name        string
	Source      string
	Key         string
	Undo        int64
	Run         int64
	Diagnostics []domain.ModelDiagnostics
}

func (l *Library) commit(ctx context.Context, params commitOptions) (Operation, error) {
	e, name, source, key, undo, run, diagnostics := params.Entry, params.Name, params.Source, params.Key, params.Undo, params.Run, params.Diagnostics

	if err := l.checkNumberingAvailable(ctx, e.ID); err != nil {
		return Operation{}, err
	}
	if l.broken != nil {
		return Operation{}, errHalted.Wrap(l.broken)
	}
	if key == "" || len(key) > 200 {
		return Operation{}, domain.InvalidField("key", "invalid")
	}
	existing, er := l.operationByKey(ctx, key)
	if er == nil {
		if existing.Entry != e.ID || existing.New != name {
			return Operation{}, errOperationKey
		}
		if existing.Status != "committed" {
			return existing, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonOperationNotCommitted, Message: "previous operation is not committed", Details: map[string]any{"status": existing.Status}})
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
	if err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM operations WHERE status IN ('prepared','review') AND entry=?", e.ID).Scan(&blocked)
	}); err != nil {
		return Operation{}, err
	}
	if blocked > 0 {
		return Operation{}, errPendingReview
	}
	p, err := l.Path(ctx, e.ID)
	if err != nil {
		return Operation{}, err
	}
	dir, err := l.openDirectory(ctx, e.Parent)
	if err != nil {
		return Operation{}, err
	}
	defer dir.Close()
	snap, err := directorySnapshot(dir, e.Name)
	if err != nil {
		return Operation{}, err
	}
	if Identity(snap) != e.Identity || (e.Kind != "directory" && (snap.Size != e.Size || stamp(snap) != e.ModifiedNS)) {
		return Operation{}, errChanged
	}
	if name == e.Name && source == "manual" {
		return Operation{Entry: e.ID, Old: name, New: name, Status: "unchanged"}, nil
	}
	// Invalidate scans before journaling or moving, including uncertain outcomes.
	l.indexEpoch.Add(1)
	target := filepath.Join(filepath.Dir(p), name)
	// Both paths share the verified absolute library root.
	oldrel, _ := filepath.Rel(l.Root, p)
	newrel, _ := filepath.Rel(l.Root, target)
	var exchange domain.ModelDiagnostics
	if len(diagnostics) > 0 {
		exchange = diagnostics[0]
	}
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	// From the prepared journal on, the rename and its records finish together;
	// cancelling midway would leave the library halted for review.
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	res, err := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime_ns,kind,source,status,undo_of,request_key,created,run,raw_request,raw_response) VALUES(?,?,?,?,?,?,?,?,?,'prepared',?,?,?,?,?,?)", e.ID, e.Parent, e.Name, name, e.Identity, snap.Size, stamp(snap), e.Kind, source, undo, key, storage.Now(), run, exchange.Request, exchange.Response)
	})
	if err != nil {
		return Operation{}, err
	}
	oid, err := res.LastInsertId()
	if err != nil {
		l.broken = err
		return Operation{}, fmt.Errorf("read prepared operation ID: %w", err)
	}
	o, err := l.Operation(ctx, oid)
	if err != nil {
		l.broken = err
		return o, err
	}
	// Journaling can block long enough for another process to replace the leaf.
	// Retain prepared evidence on any mismatch; recovery must inspect both names.
	if err = verifyOperationFile(dir, e.Name, o); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonJournalSourceChanged, Message: "source changed after journaling; restart to review"}).Wrap(err)
	}
	if name != e.Name {
		if je := rename.NoReplaceAt(int(dir.Fd()), e.Name, name); je != nil {
			_, dbErr := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(ctx, "UPDATE operations SET status='failed',error=? WHERE id=?", domain.RecordText(je), oid)
			})
			if dbErr != nil {
				l.broken = dbErr
			}

			return o, je
		}
		de := dir.Sync()
		if de != nil {
			l.broken = de
			return o, domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonRenameSyncFailed, Message: "renamed, but syncing the directory failed; restart to review"}).Wrap(de)
		}
	}
	if _, err = l.Path(ctx, e.Parent); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonRenameParentChanged, Message: "renamed, but the parent directory changed; restart to review"}).Wrap(err)
	}
	if err = verifyOperationFile(dir, name, o); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonRenameResultChanged, Message: "renamed file identity or content changed; restart to review"}).Wrap(err)
	}
	if err = l.finish(ctx, o, oldrel, newrel); err != nil {
		l.broken = err
		return o, domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonRenameUnconfirmed, Message: "file may be renamed but the result is unconfirmed; restart to recover"}).Wrap(err)
	}
	o.Status = "committed"
	return o, nil
}

func verifyOperationFile(dir *os.File, name string, o Operation) error {
	snap, err := directorySnapshot(dir, name)
	if err != nil {
		return err
	}
	if Identity(snap) != o.Identity || (o.Kind != "directory" && (snap.Size != o.Size || stamp(snap) != o.ModifiedNS)) {
		return errors.New("file identity or content does not match the operation journal")
	}
	return nil
}
func (l *Library) finish(ctx context.Context, o Operation, oldrel, newrel string) error {
	return l.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if err = l.finishTx(ctx, finishTxOptions{Tx: tx, O: o, Oldrel: oldrel, Newrel: newrel}); err != nil {
			return err
		}
		// Revalidate after database writes as well: they can wait on storage while
		// another process changes the renamed leaf. Roll back the success journal.
		dir, err := l.openDirectory(ctx, o.Parent)
		if err != nil {
			return err
		}
		defer dir.Close()
		if err = verifyOperationFile(dir, o.New, o); err != nil {
			return err
		}
		if _, err = l.Path(ctx, o.Parent); err != nil {
			return err
		}
		return nil
	})
}

// finishTxOptions groups the inputs for finishTx.
type finishTxOptions struct {
	Tx     *sql.Tx
	O      Operation
	Oldrel string
	Newrel string
}

func (l *Library) finishTx(ctx context.Context, params finishTxOptions) error {
	tx, o, oldrel, newrel := params.Tx, params.O, params.Oldrel, params.Newrel

	var err error
	if _, err = tx.ExecContext(ctx, "UPDATE entries SET name=?,version=version+1,size=?,mtime_ns=? WHERE id=?", o.New, o.Size, o.ModifiedNS, o.Entry); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE operations SET status='committed' WHERE id=?", o.ID); err != nil {
		return err
	}
	if o.UndoOf != 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE operations SET undone=1 WHERE id=?", o.UndoOf); err != nil {
			return err
		}
	}
	if o.Kind == "file" && o.Source == "restore" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM marks WHERE identity=? AND size=? AND mtime_ns=?", o.Identity, o.Size, o.ModifiedNS); err != nil {
			return err
		}
	} else if o.Kind == "file" {
		keepMark := IsImage(o.New) || IsAudio(o.New)
		if _, err = tx.ExecContext(ctx, "INSERT INTO marks VALUES(?,?,?,?,?) ON CONFLICT(identity,size,mtime_ns) DO UPDATE SET source=CASE WHEN marks.source='marked' AND ? THEN marks.source ELSE excluded.source END,operation=CASE WHEN marks.source='marked' AND ? THEN marks.operation ELSE excluded.operation END", o.Identity, o.Size, o.ModifiedNS, o.Source, o.ID, keepMark, keepMark); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(category,action,entry,operation,run,old_path,new_path,created,raw_request,raw_response) VALUES('success',?,?,?,?,?,?,?,?,?)", o.Source, o.Entry, o.ID, o.Run, oldrel, newrel, storage.Now(), o.RawRequest, o.RawResponse); err != nil {
		return err
	}
	// A committed automatic rename resolves earlier naming failures for this entry.
	if o.Source == "automatic" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE category='failure' AND action='automatic' AND entry=?", o.Entry); err != nil {
			return err
		}
	}
	// Preserve the generation while updating the sorted index.
	// Clients displaying an existing page patch entries by ID without reordering.
	if _, err = tx.ExecContext(ctx, "UPDATE directory_items SET name=? WHERE dir=? AND entry=?", o.New, o.Parent, o.Entry); err != nil {
		return err
	}
	return nil
}

// EventInput groups the inputs for Event.
type EventInput struct {
	Category  string
	Action    string
	Entry     int64
	Operation int64
	Run       int64
	Old       string
	New       string
	Message   string
}

// Event records a library event.
func (l *Library) Event(ctx context.Context, params EventInput) error {
	category, action, entry, operation, run, old, new, message := params.Category, params.Action, params.Entry, params.Operation, params.Run, params.Old, params.New, params.Message

	_, err := storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "INSERT INTO events(category,action,entry,operation,run,old_path,new_path,message,created) VALUES(?,?,?,?,?,?,?,?,?)", category, action, entry, operation, run, old, new, message, storage.Now())
	})
	return err
}

// Recover resolves operations interrupted after preparation.
func (l *Library) Recover(ctx context.Context) error {
	ops := []Operation{}
	err := l.db.View(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE status='prepared'")
		if err != nil {
			return err
		}
		ops, err = storage.CollectRows(rows, func(rows *sql.Rows) (Operation, error) { return scanOp(rows) })
		return err
	})
	if err != nil {
		return err
	}
	for _, o := range ops {
		parent, pe := l.Path(ctx, o.Parent)
		if pe != nil {
			if _, err = storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(ctx, "UPDATE operations SET status='review',error=? WHERE id=?", domain.Record{Code: "parent_unverifiable", Message: "parent directory cannot be verified"}.Text(), o.ID)
			}); err != nil {
				return err
			}
			continue
		}
		old, new := filepath.Join(parent, o.Old), filepath.Join(parent, o.New)
		a, ae := domain.StatSnapshot(old)
		b, be := domain.StatSnapshot(new)
		match := func(s domain.Snapshot) bool {
			return Identity(s) == o.Identity && (o.Kind == "directory" || (s.Size == o.Size && stamp(s) == o.ModifiedNS))
		}
		// Recovery paths share the verified absolute library root.
		oldrel, _ := filepath.Rel(l.Root, old)
		newrel, _ := filepath.Rel(l.Root, new)
		if (errors.Is(ae, fs.ErrNotExist) || o.Old == o.New) && be == nil && match(b) {
			if err = l.finish(ctx, o, oldrel, newrel); err != nil {
				return err
			}
			continue
		}
		status, msg := "review", domain.Record{Code: "rename_evidence_mismatch", Message: "paths before and after the rename disagree; review needed"}.Text()
		if ae == nil && match(a) && errors.Is(be, fs.ErrNotExist) {
			status = "interrupted"
			msg = domain.Record{Code: "rename_not_started", Message: "rename did not happen; the run was interrupted"}.Text()
		}
		if _, err = storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "UPDATE operations SET status=?,error=? WHERE id=?", status, msg, o.ID)
		}); err != nil {
			return err
		}
		if err = l.Event(ctx, EventInput{Category: "failure", Action: "recovery", Entry: o.Entry, Operation: o.ID, Run: o.Run, Old: oldrel, New: newrel, Message: msg}); err != nil {
			return err
		}
	}
	return nil
}

// CheckAvailable blocks uncertain filesystem operations on the entry and related directories.
func (l *Library) CheckAvailable(ctx context.Context, id int64) error {
	if err := l.checkNumberingAvailable(ctx, id); err != nil {
		return err
	}
	var pending bool
	if err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations WHERE status IN ('prepared','review'))").Scan(&pending)
	}); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	var n int
	err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1), ancestors(id,parent) AS (SELECT id,parent FROM entries WHERE id=? UNION ALL SELECT e.id,e.parent FROM entries e JOIN ancestors a ON e.id=a.parent) SELECT count(*) FROM operations WHERE status IN ('prepared','review') AND (entry IN (SELECT id FROM descendants) OR entry IN (SELECT id FROM ancestors))`, id, id).Scan(&n)
	})
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

func (l *Library) operationByKey(ctx context.Context, key string) (Operation, error) {
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) (Operation, error) {
		return scanOp(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE request_key=?", key))
	})
}
