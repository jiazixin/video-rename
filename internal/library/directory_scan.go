package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// ErrDirectoryScanChanged asks the caller to retry instead of publishing stale evidence.
// A mismatch alone does not prove that a user or program changed the directory.
var ErrDirectoryScanChanged = domain.Fail(domain.KindConflict, "scan_changed", "directory scan check mismatch; retry later")

// ScanDirectoryResult summarizes a directory scan. Counts refer to indexed
// entries; Files counts regular files of every media type.
// Replacing a name with another identity counts as one removal and one addition.
type ScanDirectoryResult struct {
	Directories                    []int64
	Files, Added, Changed, Removed int64
}

type directoryScanLock struct {
	gate  chan struct{}
	users int
}

// All scan callers serialize the same directory, without a global lock
// while waiting. Refcounts avoid keeping a lock for every previously visited ID.
func (l *Library) lockDirectoryScan(ctx context.Context, id int64) (func(), error) {
	l.refreshMu.Lock()
	if l.dirScans == nil {
		l.dirScans = make(map[int64]*directoryScanLock)
	}
	lock := l.dirScans[id]
	if lock == nil {
		lock = &directoryScanLock{gate: make(chan struct{}, 1)}
		l.dirScans[id] = lock
	}
	lock.users++
	l.refreshMu.Unlock()
	unref := func() {
		l.refreshMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(l.dirScans, id)
		}
		l.refreshMu.Unlock()
	}
	select {
	case lock.gate <- struct{}{}:
		return func() { <-lock.gate; unref() }, nil
	case <-ctx.Done():
		unref()
		return nil, ctx.Err()
	}
}

type scanBaseline struct {
	chain      []Entry
	epoch      uint64
	generation int64
}

type scanOrphan struct {
	entry, parent int64
	observed      string
}

const promoteScanCandidatesSQL = `UPDATE entries SET present=1,observed=?
 WHERE id IN (SELECT s.entry FROM directory_scan.snapshot s LEFT JOIN directory_scan.baseline b ON b.id=s.entry WHERE b.id IS NULL)
 AND parent=? AND observed=? AND present=0`

// ScanDirectory performs two bounded metadata passes outside Library.mu. A local
// disposable SQLite file holds the observations, so large directories do not
// require an in-memory copy. Only changed directories build a new generation.
func (l *Library) ScanDirectory(ctx context.Context, id int64) (ScanDirectoryResult, error) {
	return l.scanDirectory(ctx, id, func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) })
}

// DirectoryScanner reuses one bounded, disposable SQLite stage for a recursive
// round. Directory evidence and the index baseline are captured afresh on every
// call; no filesystem or epoch evidence is cached between directories.
type DirectoryScanner struct {
	library *Library
	stage   *sql.DB
	path    string
	mu      sync.Mutex
}

// NewDirectoryScanner creates a scanner that indexes directories in a private
// staging area.
func (l *Library) NewDirectoryScanner(ctx context.Context) (*DirectoryScanner, error) {
	stage, path, err := newScanStage(ctx)
	if err != nil {
		return nil, err
	}
	if err = l.captureScanOrphans(ctx, stage); err != nil {
		stage.Close()
		os.Remove(path)
		return nil, err
	}
	return &DirectoryScanner{library: l, stage: stage, path: path}, nil
}

// Close waits for an in-flight directory before removing the local stage.
func (s *DirectoryScanner) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage == nil {
		return nil
	}
	err := s.stage.Close()
	s.stage = nil
	return errors.Join(err, os.Remove(s.path))
}

// ScanDirectory indexes directory id and reports what changed.
func (s *DirectoryScanner) ScanDirectory(ctx context.Context, id int64) (ScanDirectoryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ScanDirectoryResult{}, err
	}
	if s.stage == nil {
		return ScanDirectoryResult{}, errors.New("directory scan staging is closed")
	}
	// A failed or cancelled directory may have left local observations. Reset
	// all three tables before capturing any evidence for the next directory.
	if _, err := s.stage.ExecContext(ctx, "DELETE FROM baseline; DELETE FROM snapshot; DELETE FROM verification"); err != nil {
		return ScanDirectoryResult{}, err
	}
	return s.library.scanDirectoryWithStage(ctx, id, s.stage, s.path,
		func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) }, scanPinnedDirectoryEntry)
}

func (l *Library) scanDirectory(ctx context.Context, id int64, read func(*os.File, int) ([]os.DirEntry, error)) (ScanDirectoryResult, error) {
	return l.scanDirectoryWithMetadata(ctx, id, read, scanPinnedDirectoryEntry)
}

func (l *Library) scanDirectoryWithMetadata(ctx context.Context, id int64, read func(*os.File, int) ([]os.DirEntry, error), inspect func(*os.File, os.DirEntry) (scannedDirectoryEntry, error)) (ScanDirectoryResult, error) {
	stage, path, err := newScanStage(ctx)
	if err != nil {
		return ScanDirectoryResult{}, err
	}
	defer func() { stage.Close(); _ = os.Remove(path) }()
	if err = l.captureScanOrphans(ctx, stage); err != nil {
		return ScanDirectoryResult{}, err
	}
	return l.scanDirectoryWithStage(ctx, id, stage, path, read, inspect)
}

func (l *Library) scanDirectoryWithStage(ctx context.Context, id int64, stage *sql.DB, path string, read func(*os.File, int) ([]os.DirEntry, error), inspect func(*os.File, os.DirEntry) (scannedDirectoryEntry, error)) (result ScanDirectoryResult, err error) {
	release, err := l.lockDirectoryScan(ctx, id)
	if err != nil {
		return result, err
	}
	defer release()
	l.refreshMu.Lock()
	if l.scanning == nil {
		l.scanning = make(map[int64]bool)
	}
	l.scanning[id] = true
	l.refreshMu.Unlock()
	defer func() {
		l.refreshMu.Lock()
		delete(l.scanning, id)
		l.refreshMu.Unlock()
		if err != nil {
			failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = l.DB.ExecContext(failureCtx, "INSERT INTO directory_state(dir,error) VALUES(?,?) ON CONFLICT(dir) DO UPDATE SET error=excluded.error", id, domain.RecordText(err))
		}
	}()
	baseline, err := l.captureScanBaseline(ctx, id, stage)
	if err != nil {
		return result, err
	}
	pinned, err := openScanDirectory(l.Root, baseline.chain)
	if err != nil {
		return result, err
	}
	defer pinned.Close()
	var observed domain.Snapshot
	for _, table := range []string{"snapshot", "verification"} {
		dir := pinned
		if table == "verification" {
			// An independent enumeration offset on the same pinned directory
			// avoids reopening/statting every ancestor for the second pass.
			// The complete ancestor chain is independently reopened before
			// publication below, so path replacement still invalidates evidence.
			fd, openErr := unix.Openat(int(pinned.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr != nil {
				return result, openErr
			}
			dir = os.NewFile(uintptr(fd), pinned.Name())
		}
		before, scanErr := scanDirectorySnapshot(dir)
		if scanErr == nil && table == "verification" && !before.Same(observed) {
			scanErr = scanSnapshotDifference("directory attributes between the two reads", observed, before)
		}
		if scanErr == nil {
			scanErr = collectScanMetadata(ctx, stage, table, dir, read, inspect)
		}
		after, statErr := scanDirectorySnapshot(dir)
		if dir != pinned {
			dir.Close()
		}
		if scanErr != nil {
			return result, scanErr
		}
		if statErr != nil {
			return result, statErr
		}
		if !before.Same(after) {
			phase := "directory attributes during the first read"
			if table == "verification" {
				phase = "directory attributes during the second read"
			}
			return result, scanSnapshotDifference(phase, before, after)
		}
		observed = after
	}
	if err = scanMetadataDifference(ctx, stage); err != nil {
		return result, err
	}
	if _, err = stage.ExecContext(ctx, `UPDATE snapshot SET entry=COALESCE((SELECT id FROM baseline b WHERE b.name=snapshot.name AND b.identity=snapshot.identity),0)`); err != nil {
		return result, err
	}
	if err = stage.QueryRowContext(ctx, `SELECT
	 (SELECT count(*) FROM snapshot WHERE kind='file'),
	 (SELECT count(*) FROM snapshot WHERE entry=0),
	 (SELECT count(*) FROM snapshot s JOIN baseline b ON s.entry=b.id WHERE s.kind<>b.kind OR s.size<>b.size OR s.mtime<>b.mtime),
	 (SELECT count(*) FROM baseline b WHERE NOT EXISTS(SELECT 1 FROM snapshot s WHERE s.entry=b.id))`).Scan(&result.Files, &result.Added, &result.Changed, &result.Removed); err != nil {
		return result, err
	}
	gen := baseline.generation
	rebuild := gen == 0 || result.Added+result.Changed+result.Removed > 0
	marker := fmt.Sprintf("directory-scan:%d:%d", id, time.Now().UnixNano())
	published := false
	if rebuild {
		gen = max(time.Now().UnixNano(), gen+1)
		defer func() {
			if !published {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				l.cleanScanStaging(cleanupCtx, stage, id, gen, marker, false)
			}
		}()
		if err = l.buildScanGeneration(ctx, id, gen, marker, stage); err != nil {
			return result, err
		}
	}
	rows, err := stage.QueryContext(ctx, "SELECT entry FROM snapshot WHERE kind='directory' ORDER BY entry")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var child int64
		if err = rows.Scan(&child); err != nil {
			break
		}
		result.Directories = append(result.Directories, child)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	// Reopen the frozen ancestor chain and check the directory after staging. NAS
	// operations stay outside the global publication lock.
	dir, err := openScanDirectory(l.Root, baseline.chain)
	if err != nil {
		return result, err
	}
	current, err := scanDirectorySnapshot(dir)
	dir.Close()
	if err != nil {
		return result, err
	}
	if !current.Same(observed) {
		return result, scanSnapshotDifference("directory attributes before publishing", observed, current)
	}
	if err = l.publishDirectoryScan(ctx, id, gen, marker, baseline, path, rebuild); err != nil {
		return result, err
	}
	published = true
	// Reclaim interrupted staging even when this directory has not changed. Old
	// generations are no longer visible; bounded writes let user operations
	// acquire both the mutex and writer between pages.
	l.cleanScanStaging(ctx, stage, id, gen, marker, true)
	return result, nil
}

func newScanStage(ctx context.Context) (*sql.DB, string, error) {
	file, err := os.CreateTemp("", "video-rename-scan-*.sqlite")
	if err != nil {
		return nil, "", err
	}
	path := file.Name()
	file.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		os.Remove(path)
		return nil, "", err
	}
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, `PRAGMA journal_mode=MEMORY; PRAGMA synchronous=OFF;
	 CREATE TABLE baseline(id INTEGER PRIMARY KEY,name TEXT NOT NULL UNIQUE,kind TEXT NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime TEXT NOT NULL,version INTEGER NOT NULL);
	 CREATE TABLE snapshot(name TEXT PRIMARY KEY,kind TEXT NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime TEXT NOT NULL,entry INTEGER NOT NULL DEFAULT 0);
	 CREATE INDEX snapshot_entry ON snapshot(entry);
	 CREATE TABLE verification(name TEXT PRIMARY KEY,kind TEXT NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime TEXT NOT NULL);
	 CREATE TABLE orphan_candidates(entry INTEGER PRIMARY KEY,parent INTEGER NOT NULL,observed TEXT NOT NULL);
	 CREATE INDEX orphan_parent_entry ON orphan_candidates(parent,entry);`)
	if err != nil {
		db.Close()
		os.Remove(path)
		return nil, "", err
	}
	return db, path, nil
}

// An interrupted candidate page can precede its directory_items rows.
// Capture them once per recursive round through the read pool, then seek the
// temporary parent index instead of rescanning all entries for every directory.
func (l *Library) captureScanOrphans(ctx context.Context, stage *sql.DB) error {
	rows, err := l.DB.Read().QueryContext(ctx, `SELECT e.id,e.parent,e.observed FROM entries e
	 WHERE e.present=0 AND e.observed GLOB 'directory-scan:*'
	 AND NOT EXISTS(SELECT 1 FROM directory_items i WHERE i.dir=e.parent AND i.entry=e.id)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	batch := make([]scanOrphan, 0, refreshPageSize)
	flush := func() error {
		tx, err := stage.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.PrepareContext(ctx, "INSERT INTO orphan_candidates VALUES(?,?,?)")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, item := range batch {
			if _, err = stmt.ExecContext(ctx, item.entry, item.parent, item.observed); err != nil {
				return err
			}
		}
		batch = batch[:0]
		return tx.Commit()
	}
	for rows.Next() {
		var item scanOrphan
		if err = rows.Scan(&item.entry, &item.parent, &item.observed); err != nil {
			return err
		}
		batch = append(batch, item)
		if len(batch) == refreshPageSize {
			if err = flush(); err != nil {
				return err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(batch) > 0 {
		return flush()
	}
	return nil
}

func (l *Library) captureScanBaseline(ctx context.Context, id int64, stage *sql.DB) (baseline scanBaseline, err error) {
	if err = l.lockNumbering(ctx); err != nil {
		return baseline, err
	}
	if l.broken != nil {
		err = errHalted.Wrap(l.broken)
	} else {
		err = l.CheckAvailable(id)
	}
	baseline.epoch = l.indexEpoch.Load()
	l.mu.Unlock()
	if err != nil {
		return baseline, err
	}
	tx, err := l.DB.Read().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return baseline, err
	}
	defer tx.Rollback()
	seen := map[int64]bool{}
	for ancestor := id; ; {
		if ancestor <= 0 || seen[ancestor] || len(baseline.chain) > 1024 {
			return baseline, errBadHierarchy
		}
		seen[ancestor] = true
		var e Entry
		if err = tx.QueryRowContext(ctx, `SELECT id,parent,name,kind,identity,size,mtime,version FROM entries WHERE id=? AND present=1`, ancestor).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.Mtime, &e.Version); err != nil {
			return baseline, err
		}
		if e.Kind != "directory" {
			return baseline, errNotDirectory
		}
		baseline.chain = append(baseline.chain, e)
		if ancestor == 1 {
			break
		}
		ancestor = e.Parent
	}
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM directory_state WHERE dir=?", id).Scan(&baseline.generation); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return baseline, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,name,kind,identity,size,mtime,version FROM entries WHERE parent=? AND present=1 ORDER BY id", id)
	if err != nil {
		return baseline, err
	}
	defer rows.Close()
	batch := make([]Entry, 0, refreshPageSize)
	flush := func() error {
		write, err := stage.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer write.Rollback()
		stmt, err := write.PrepareContext(ctx, "INSERT INTO baseline VALUES(?,?,?,?,?,?,?)")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, e := range batch {
			if _, err = stmt.ExecContext(ctx, e.ID, e.Name, e.Kind, e.Identity, e.Size, e.Mtime, e.Version); err != nil {
				return err
			}
		}
		batch = batch[:0]
		return write.Commit()
	}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.Mtime, &e.Version); err != nil {
			return baseline, err
		}
		batch = append(batch, e)
		if len(batch) == refreshPageSize {
			if err = flush(); err != nil {
				return baseline, err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return baseline, err
	}
	if len(batch) > 0 {
		err = flush()
	}
	return baseline, err
}

func openScanDirectory(root string, chain []Entry) (*os.File, error) {
	var dir *os.File
	displayPath := root
	for _, e := range slices.Backward(chain) {
		parent, name := unix.AT_FDCWD, root
		if dir != nil {
			if e.Name == "" || e.Name == "." || e.Name == ".." || filepath.Base(e.Name) != e.Name {
				dir.Close()
				return nil, errIllegalPath
			}
			parent, name = int(dir.Fd()), e.Name
			displayPath = filepath.Join(displayPath, e.Name)
		}
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if dir != nil {
			dir.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("open media directory: %w", err)
		}
		dir = os.NewFile(uintptr(fd), displayPath)
		snapshot, err := scanDirectorySnapshot(dir)
		if err != nil || Identity(snapshot) != e.Identity {
			dir.Close()
			if err != nil {
				return nil, err
			}
			return nil, errIdentityChanged
		}
	}
	return dir, nil
}

func scanDirectorySnapshot(dir *os.File) (domain.Snapshot, error) {
	info, err := dir.Stat()
	if err != nil {
		return domain.Snapshot{}, err
	}
	return domain.SnapshotFromInfo(info)
}

// Fstatat uses the pinned directory descriptor even if an ancestor path is
// concurrently renamed or replaced with a symlink. DirEntry.Info would instead
// lstat the path stored in os.File.Name and could observe another filesystem.
func scanPinnedDirectoryEntry(dir *os.File, de os.DirEntry) (scannedDirectoryEntry, error) {
	name := de.Name()
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return scannedDirectoryEntry{}, errIllegalPath
	}
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return scannedDirectoryEntry{}, err
	}
	kind := "other"
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		kind = "file"
	case unix.S_IFDIR:
		kind = "directory"
	case unix.S_IFLNK:
		kind = "symlink"
	}
	snap := domain.Snapshot{Dev: uint64(st.Dev), Ino: uint64(st.Ino), Size: st.Size, ModTime: time.Unix(st.Mtim.Sec, st.Mtim.Nsec)}
	return scannedDirectoryEntry{name, kind, Identity(snap), stamp(snap), snap.Size}, nil
}

func collectScanMetadata(ctx context.Context, stage *sql.DB, table string, dir *os.File, read func(*os.File, int) ([]os.DirEntry, error), inspectors ...func(*os.File, os.DirEntry) (scannedDirectoryEntry, error)) error {
	inspect := scanPinnedDirectoryEntry
	if len(inspectors) > 0 {
		inspect = inspectors[0]
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, readErr := read(dir, refreshPageSize)
		scanned, err := readDirectoryMetadata(ctx, batch, func(de os.DirEntry) (scannedDirectoryEntry, error) { return inspect(dir, de) })
		if err != nil {
			return err
		}
		if len(scanned) > 0 {
			tx, err := stage.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			stmt, err := tx.PrepareContext(ctx, "INSERT INTO "+table+"(name,kind,identity,size,mtime) VALUES(?,?,?,?,?)")
			if err == nil {
				for _, item := range scanned {
					if _, err = stmt.ExecContext(ctx, item.name, item.kind, item.identity, item.size, item.mtime); err != nil {
						break
					}
				}
				stmt.Close()
			}
			if err == nil {
				err = tx.Commit()
			}
			tx.Rollback()
			if err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

type stagedScanItem struct {
	scannedDirectoryEntry
	entry int64
}

func (l *Library) buildScanGeneration(ctx context.Context, id, gen int64, marker string, stage *sql.DB) error {
	var after string
	for {
		rows, err := stage.QueryContext(ctx, "SELECT name,kind,identity,size,mtime,entry FROM snapshot WHERE name>? ORDER BY name LIMIT ?", after, refreshPageSize)
		if err != nil {
			return err
		}
		items := make([]stagedScanItem, 0, refreshPageSize)
		for rows.Next() {
			var item stagedScanItem
			if err = rows.Scan(&item.name, &item.kind, &item.identity, &item.size, &item.mtime, &item.entry); err != nil {
				break
			}
			items = append(items, item)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		if err = l.writeScanGenerationPage(ctx, id, gen, marker, items); err != nil {
			return err
		}
		if err = updateScanStageEntries(ctx, stage, items); err != nil {
			return err
		}
		after = items[len(items)-1].name
	}
}

func updateScanStageEntries(ctx context.Context, stage *sql.DB, items []stagedScanItem) error {
	tx, err := stage.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE snapshot SET entry=? WHERE name=?")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		if _, err = stmt.ExecContext(ctx, item.entry, item.name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l *Library) writeScanGenerationPage(ctx context.Context, id, gen int64, marker string, items []stagedScanItem) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entryStmt, err := tx.PrepareContext(ctx, "INSERT INTO entries(parent,name,kind,identity,size,mtime,present,observed) VALUES(?,?,?,?,?,?,0,?)")
	if err != nil {
		return err
	}
	defer entryStmt.Close()
	itemStmt, err := tx.PrepareContext(ctx, "INSERT INTO directory_items VALUES(?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer itemStmt.Close()
	for i := range items {
		item := &items[i]
		if item.entry == 0 {
			res, err := entryStmt.ExecContext(ctx, id, item.name, item.kind, item.identity, item.size, item.mtime, marker)
			if err != nil {
				return err
			}
			if item.entry, err = res.LastInsertId(); err != nil {
				return err
			}
		}
		if _, err = itemStmt.ExecContext(ctx, id, gen, item.entry, item.name, item.kind, item.size, item.mtime); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l *Library) publishDirectoryScan(ctx context.Context, id, gen int64, marker string, baseline scanBaseline, stagePath string, rebuild bool) error {
	if err := l.lockNumbering(ctx); err != nil {
		return err
	}
	defer l.mu.Unlock()
	if l.broken != nil {
		return errHalted.Wrap(l.broken)
	}
	if err := l.CheckAvailable(id); err != nil {
		return err
	}
	conn, err := l.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "ATTACH DATABASE ? AS directory_scan", stagePath); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE directory_scan")
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var generation int64
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM directory_state WHERE dir=?", id).Scan(&generation); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if generation != baseline.generation {
		return ErrDirectoryScanChanged.Wrap(fmt.Errorf("directory index generation %d -> %d", baseline.generation, generation))
	}
	if l.indexEpoch.Load() != baseline.epoch {
		if err = validateScanBaseline(ctx, tx, id, baseline); err != nil {
			return err
		}
	}
	if rebuild {
		if _, err = tx.ExecContext(ctx, `UPDATE entries SET present=0 WHERE id IN
		 (SELECT b.id FROM directory_scan.baseline b WHERE NOT EXISTS(SELECT 1 FROM directory_scan.snapshot s WHERE s.entry=b.id))`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, promoteScanCandidatesSQL, storage.Now(), id, marker); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE entries AS e SET
		 version=e.version+CASE WHEN e.size<>s.size OR e.mtime<>s.mtime THEN 1 ELSE 0 END,
		 kind=s.kind,size=s.size,mtime=s.mtime,observed=?
		 FROM directory_scan.snapshot s JOIN directory_scan.baseline b ON s.entry=b.id
		 WHERE e.id=s.entry AND (s.kind<>b.kind OR s.size<>b.size OR s.mtime<>b.mtime)`, storage.Now()); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO directory_state(dir,generation,refreshed,error) VALUES(?,?,?,'')
	 ON CONFLICT(dir) DO UPDATE SET generation=excluded.generation,refreshed=excluded.refreshed,error=''`, id, gen, storage.Now()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if rebuild {
		l.indexEpoch.Add(1)
	}
	return nil
}

func (l *Library) cleanScanStaging(ctx context.Context, stage *sql.DB, id, gen int64, marker string, published bool) {
	comparison := "="
	if published {
		comparison = "<>"
	}
	for ctx.Err() == nil {
		count, err := l.cleanScanGenerationPage(ctx, id, gen, marker, comparison, published)
		if err != nil {
			return
		}
		if count == 0 {
			break
		}
	}
	var after int64
	for ctx.Err() == nil {
		rows, err := stage.QueryContext(ctx, "SELECT entry,observed FROM orphan_candidates WHERE parent=? AND entry>? ORDER BY entry LIMIT ?", id, after, refreshPageSize)
		if err != nil {
			return
		}
		orphans := make([]scanOrphan, 0, refreshPageSize)
		for rows.Next() {
			var item scanOrphan
			if err = rows.Scan(&item.entry, &item.observed); err != nil {
				break
			}
			orphans = append(orphans, item)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil || len(orphans) == 0 {
			return
		}
		tx, err := l.DB.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		err = deleteCapturedScanOrphans(ctx, tx, id, marker, published, orphans)
		if err == nil {
			err = tx.Commit()
		}
		tx.Rollback()
		if err != nil {
			return
		}
		after = orphans[len(orphans)-1].entry
	}
}

// Removing obsolete generation rows and their invisible candidates in one
// transaction prevents cleanup interruption from creating new orphan entries.
// The indexed generation rows also identify candidates whose page committed
// before a local stage update failed.
func (l *Library) cleanScanGenerationPage(ctx context.Context, id, gen int64, marker, comparison string, published bool) (int, error) {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT rowid,entry FROM directory_items WHERE dir=? AND generation"+comparison+"? AND generation<>COALESCE((SELECT generation FROM directory_state WHERE dir=?),0) LIMIT ?", id, gen, id, refreshPageSize)
	if err != nil {
		return 0, err
	}
	var rowIDs, entries []int64
	for rows.Next() {
		var row, entry int64
		if err = rows.Scan(&row, &entry); err != nil {
			break
		}
		rowIDs = append(rowIDs, row)
		entries = append(entries, entry)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(rowIDs) == 0 {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM directory_items WHERE rowid=?")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, row := range rowIDs {
		if _, err = stmt.ExecContext(ctx, row); err != nil {
			return 0, err
		}
	}
	if err = deleteScanCandidates(ctx, tx, id, marker, published, entries); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return len(rowIDs), nil
}

func deleteScanCandidates(ctx context.Context, tx *sql.Tx, id int64, marker string, published bool, entries []int64) error {
	filter, value := "observed=?", marker
	if published {
		filter, value = "observed LIKE ?", "directory-scan:%"
	}
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM entries WHERE id=? AND parent=? AND present=0 AND "+filter+" AND NOT EXISTS(SELECT 1 FROM directory_items i WHERE i.dir=entries.parent AND i.entry=entries.id)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, entry := range entries {
		if _, err = stmt.ExecContext(ctx, entry, id, value); err != nil {
			return err
		}
	}
	return nil
}

func deleteCapturedScanOrphans(ctx context.Context, tx *sql.Tx, id int64, marker string, published bool, orphans []scanOrphan) error {
	stmt, err := tx.PrepareContext(ctx, `DELETE FROM entries WHERE id=? AND parent=? AND present=0 AND observed=?
	 AND NOT EXISTS(SELECT 1 FROM directory_items i WHERE i.dir=entries.parent AND i.entry=entries.id)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range orphans {
		if !published && item.observed != marker {
			continue
		}
		if _, err = stmt.ExecContext(ctx, item.entry, id, item.observed); err != nil {
			return err
		}
	}
	return nil
}
