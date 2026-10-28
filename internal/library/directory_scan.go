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
var ErrDirectoryScanChanged = domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonScanChanged, Message: "directory scan check mismatch; retry later"})

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
// mu serializes scans and protects the stage's lifetime.
type DirectoryScanner struct {
	library *Library
	stage   *storage.ScanStore
	mu      sync.Mutex
}

// NewDirectoryScanner creates a scanner that indexes directories in a private
// staging area.
func (l *Library) NewDirectoryScanner(ctx context.Context) (*DirectoryScanner, error) {
	stage, err := newScanStage(ctx)
	if err != nil {
		return nil, err
	}
	if err = l.captureScanOrphans(ctx, stage); err != nil {
		return nil, errors.Join(err, stage.Close())
	}
	return &DirectoryScanner{library: l, stage: stage}, nil
}

// Close waits for an in-flight directory before removing the local stage.
func (s *DirectoryScanner) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stage == nil {
		return nil
	}
	if err := s.stage.Close(); err != nil {
		return err
	}
	s.stage = nil
	return nil
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
	if err := s.stage.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM baseline; DELETE FROM snapshot; DELETE FROM verification")
		return err
	}); err != nil {
		return ScanDirectoryResult{}, err
	}
	return s.library.scanDirectoryWithStage(ctx, scanDirectoryWithStageOptions{ID: id, Stage: s.stage, Read: func(dir *os.File, n int) ([]os.DirEntry, error) { return dir.ReadDir(n) }, Inspect: scanPinnedDirectoryEntry})
}

func (l *Library) scanDirectory(ctx context.Context, id int64, read func(*os.File, int) ([]os.DirEntry, error)) (ScanDirectoryResult, error) {
	return l.scanDirectoryWithMetadata(ctx, id, read, scanPinnedDirectoryEntry)
}

func (l *Library) scanDirectoryWithMetadata(ctx context.Context, id int64, read func(*os.File, int) ([]os.DirEntry, error), inspect func(*os.File, os.DirEntry) (scannedDirectoryEntry, error)) (result ScanDirectoryResult, err error) {
	stage, err := newScanStage(ctx)
	if err != nil {
		return ScanDirectoryResult{}, err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	if err = l.captureScanOrphans(ctx, stage); err != nil {
		return ScanDirectoryResult{}, err
	}
	return l.scanDirectoryWithStage(ctx, scanDirectoryWithStageOptions{ID: id, Stage: stage, Read: read, Inspect: inspect})
}

// scanDirectoryWithStageOptions groups the inputs for scanDirectoryWithStage.
type scanDirectoryWithStageOptions struct {
	ID      int64
	Stage   *storage.ScanStore
	Read    func(*os.File, int) ([]os.DirEntry, error)
	Inspect func(*os.File, os.DirEntry) (scannedDirectoryEntry, error)
}

func (l *Library) scanDirectoryWithStage(ctx context.Context, params scanDirectoryWithStageOptions) (result ScanDirectoryResult, err error) {
	id, stage, read, inspect := params.ID, params.Stage, params.Read, params.Inspect

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
			if recordErr := l.db.Write(failureCtx, func(tx *sql.Tx) error {
				_, writeErr := tx.ExecContext(failureCtx, "INSERT INTO directory_state(dir,error) VALUES(?,?) ON CONFLICT(dir) DO UPDATE SET error=excluded.error", id, domain.RecordText(err))
				return writeErr
			}); recordErr != nil {
				err = errors.Join(err, fmt.Errorf("record directory %d scan failure: %w", id, recordErr))
			}
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
	defer func() { err = errors.Join(err, pinned.Close()) }()
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
			scanErr = collectScanMetadata(ctx, collectScanMetadataOptions{Stage: stage, Table: table, Dir: dir, Read: read, Inspectors: []func(*os.File, os.DirEntry) (scannedDirectoryEntry, error){inspect}})
		}
		after, statErr := scanDirectorySnapshot(dir)
		if dir != pinned {
			statErr = errors.Join(statErr, dir.Close())
		}
		if scanErr != nil || statErr != nil {
			return result, errors.Join(scanErr, statErr)
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
	if err = stage.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE snapshot SET entry=COALESCE((SELECT id FROM baseline b WHERE b.name=snapshot.name AND b.identity=snapshot.identity),0)`)
		return err
	}); err != nil {
		return result, err
	}
	if err = stage.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT
	 (SELECT count(*) FROM snapshot WHERE kind='file'),
	 (SELECT count(*) FROM snapshot WHERE entry=0),
	 (SELECT count(*) FROM snapshot s JOIN baseline b ON s.entry=b.id WHERE s.kind<>b.kind OR s.size<>b.size OR s.mtime_ns<>b.mtime_ns),
	 (SELECT count(*) FROM baseline b WHERE NOT EXISTS(SELECT 1 FROM snapshot s WHERE s.entry=b.id))`).Scan(&result.Files, &result.Added, &result.Changed, &result.Removed)
	}); err != nil {
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
				err = errors.Join(err, l.cleanScanStaging(cleanupCtx, cleanScanStagingOptions{Stage: stage, ID: id, Gen: gen, Marker: marker, Published: false}))
			}
		}()
		if err = l.buildScanGeneration(ctx, buildScanGenerationOptions{ID: id, Gen: gen, Marker: marker, Stage: stage}); err != nil {
			return result, err
		}
	}
	if err = stage.View(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT entry FROM snapshot WHERE kind='directory' ORDER BY entry")
		if err != nil {
			return err
		}
		result.Directories, err = storage.CollectRows(rows, func(rows *sql.Rows) (int64, error) {
			var child int64
			err := rows.Scan(&child)
			return child, err
		})
		return err
	}); err != nil {
		return result, err
	}
	// Reopen the frozen ancestor chain and check the directory after staging. NAS
	// operations stay outside the global publication lock.
	dir, err := openScanDirectory(l.Root, baseline.chain)
	if err != nil {
		return result, err
	}
	current, err := scanDirectorySnapshot(dir)
	err = errors.Join(err, dir.Close())
	if err != nil {
		return result, err
	}
	if !current.Same(observed) {
		return result, scanSnapshotDifference("directory attributes before publishing", observed, current)
	}
	if err = l.publishDirectoryScan(ctx, publishDirectoryScanOptions{ID: id, Gen: gen, Marker: marker, Baseline: baseline, StagePath: stage.Path(), Rebuild: rebuild}); err != nil {
		return result, err
	}
	published = true
	// Reclaim interrupted staging even when this directory has not changed. Old
	// generations are no longer visible; bounded writes let user operations
	// acquire both the mutex and writer between pages.
	return result, l.cleanScanStaging(ctx, cleanScanStagingOptions{Stage: stage, ID: id, Gen: gen, Marker: marker, Published: true})
}

const scanStageSchema = `CREATE TABLE baseline(id INTEGER PRIMARY KEY,name TEXT NOT NULL UNIQUE,kind TEXT NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime_ns INTEGER NOT NULL,version INTEGER NOT NULL);
	 CREATE TABLE snapshot(name TEXT PRIMARY KEY,kind TEXT NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime_ns INTEGER NOT NULL,entry INTEGER NOT NULL DEFAULT 0);
	 CREATE INDEX snapshot_entry ON snapshot(entry);
	 CREATE TABLE verification(name TEXT PRIMARY KEY,kind TEXT NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime_ns INTEGER NOT NULL);
	 CREATE TABLE orphan_candidates(entry INTEGER PRIMARY KEY,parent INTEGER NOT NULL,observed TEXT NOT NULL);
	 CREATE INDEX orphan_parent_entry ON orphan_candidates(parent,entry);`

func newScanStage(ctx context.Context) (*storage.ScanStore, error) {
	return storage.NewScan(ctx, scanStageSchema)
}

// An interrupted candidate page can precede its directory_items rows.
// Capture them once per recursive round through the read pool, then seek the
// temporary parent index instead of rescanning all entries for every directory.
func (l *Library) captureScanOrphans(ctx context.Context, stage *storage.ScanStore) error {
	return l.db.View(ctx, func(tx *sql.Tx) (err error) {
		rows, err := tx.QueryContext(ctx, `SELECT e.id,e.parent,e.observed FROM entries e
	 WHERE e.present=0 AND e.observed GLOB 'directory-scan:*'
	 AND NOT EXISTS(SELECT 1 FROM directory_items i WHERE i.dir=e.parent AND i.entry=e.id)`)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, rows.Close()) }()
		batch := make([]scanOrphan, 0, refreshPageSize)
		flush := func() error {
			if err := stage.Write(ctx, func(tx *sql.Tx) (err error) {
				stmt, err := tx.PrepareContext(ctx, "INSERT INTO orphan_candidates VALUES(?,?,?)")
				if err != nil {
					return err
				}
				defer func() { err = errors.Join(err, stmt.Close()) }()
				for _, item := range batch {
					if _, err := stmt.ExecContext(ctx, item.entry, item.parent, item.observed); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			batch = batch[:0]
			return nil
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
	})
}

func (l *Library) captureScanBaseline(ctx context.Context, id int64, stage *storage.ScanStore) (baseline scanBaseline, err error) {
	if err = l.lockNumbering(ctx); err != nil {
		return baseline, err
	}
	if l.broken != nil {
		err = errHalted.Wrap(l.broken)
	} else {
		err = l.CheckAvailable(ctx, id)
	}
	baseline.epoch = l.indexEpoch.Load()
	l.mu.Unlock()
	if err != nil {
		return baseline, err
	}
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) (_ scanBaseline, err error) {
		seen := map[int64]bool{}
		for ancestor := id; ; {
			if ancestor <= 0 || seen[ancestor] || len(baseline.chain) > 1024 {
				return baseline, errBadHierarchy
			}
			seen[ancestor] = true
			var e Entry
			if err = tx.QueryRowContext(ctx, `SELECT id,parent,name,kind,identity,size,mtime_ns,version FROM entries WHERE id=? AND present=1`, ancestor).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.ModifiedNS, &e.Version); err != nil {
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
		rows, err := tx.QueryContext(ctx, "SELECT id,name,kind,identity,size,mtime_ns,version FROM entries WHERE parent=? AND present=1 ORDER BY id", id)
		if err != nil {
			return baseline, err
		}
		defer func() { err = errors.Join(err, rows.Close()) }()
		batch := make([]Entry, 0, refreshPageSize)
		flush := func() error {
			if err := stage.Write(ctx, func(tx *sql.Tx) (err error) {
				stmt, err := tx.PrepareContext(ctx, "INSERT INTO baseline VALUES(?,?,?,?,?,?,?)")
				if err != nil {
					return err
				}
				defer func() { err = errors.Join(err, stmt.Close()) }()
				for _, e := range batch {
					if _, err := stmt.ExecContext(ctx, e.ID, e.Name, e.Kind, e.Identity, e.Size, e.ModifiedNS, e.Version); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			batch = batch[:0]
			return nil
		}
		for rows.Next() {
			var e Entry
			if err = rows.Scan(&e.ID, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.ModifiedNS, &e.Version); err != nil {
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
	})
}

func openScanDirectory(root string, chain []Entry) (*os.File, error) {
	var dir *os.File
	displayPath := root
	for _, e := range slices.Backward(chain) {
		parent, name := unix.AT_FDCWD, root
		if dir != nil {
			if e.Name == "" || e.Name == "." || e.Name == ".." || filepath.Base(e.Name) != e.Name {
				return nil, errors.Join(errIllegalPath, dir.Close())
			}
			parent, name = int(dir.Fd()), e.Name
			displayPath = filepath.Join(displayPath, e.Name)
		}
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if dir != nil {
			err = errors.Join(err, dir.Close())
		}
		if err != nil {
			if fd >= 0 {
				err = errors.Join(err, unix.Close(fd))
			}
			return nil, fmt.Errorf("open media directory: %w", err)
		}
		dir = os.NewFile(uintptr(fd), displayPath)
		snapshot, err := scanDirectorySnapshot(dir)
		if err != nil || Identity(snapshot) != e.Identity {
			if err == nil {
				err = errIdentityChanged
			}
			return nil, errors.Join(err, dir.Close())
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

// collectScanMetadataOptions groups the inputs for collectScanMetadata.
type collectScanMetadataOptions struct {
	Stage      *storage.ScanStore
	Table      string
	Dir        *os.File
	Read       func(*os.File, int) ([]os.DirEntry, error)
	Inspectors []func(*os.File, os.DirEntry) (scannedDirectoryEntry, error)
}

func collectScanMetadata(ctx context.Context, params collectScanMetadataOptions) error {
	stage, table, dir, read, inspectors := params.Stage, params.Table, params.Dir, params.Read, params.Inspectors

	var statement string
	switch table {
	case "snapshot":
		statement = "INSERT INTO snapshot(name,kind,identity,size,mtime_ns) VALUES(?,?,?,?,?)"
	case "verification":
		statement = "INSERT INTO verification(name,kind,identity,size,mtime_ns) VALUES(?,?,?,?,?)"
	default:
		return fmt.Errorf("unknown scan metadata table %q", table)
	}
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
			if err := stage.Write(ctx, func(tx *sql.Tx) (err error) {
				stmt, err := tx.PrepareContext(ctx, statement)
				if err != nil {
					return err
				}
				defer func() { err = errors.Join(err, stmt.Close()) }()
				for _, item := range scanned {
					if _, err := stmt.ExecContext(ctx, item.name, item.kind, item.identity, item.size, item.mtime); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
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

// buildScanGenerationOptions groups the inputs for buildScanGeneration.
type buildScanGenerationOptions struct {
	ID     int64
	Gen    int64
	Marker string
	Stage  *storage.ScanStore
}

func (l *Library) buildScanGeneration(ctx context.Context, params buildScanGenerationOptions) error {
	id, gen, marker, stage := params.ID, params.Gen, params.Marker, params.Stage

	var after string
	for {
		var items []stagedScanItem
		if err := stage.View(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, "SELECT name,kind,identity,size,mtime_ns,entry FROM snapshot WHERE name>? ORDER BY name LIMIT ?", after, refreshPageSize)
			if err != nil {
				return err
			}
			items, err = storage.CollectRows(rows, func(rows *sql.Rows) (stagedScanItem, error) {
				var item stagedScanItem
				err := rows.Scan(&item.name, &item.kind, &item.identity, &item.size, &item.mtime, &item.entry)
				return item, err
			})
			return err
		}); err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		if err := l.writeScanGenerationPage(ctx, writeScanGenerationPageOptions{ID: id, Gen: gen, Marker: marker, Items: items}); err != nil {
			return err
		}
		if err := updateScanStageEntries(ctx, stage, items); err != nil {
			return err
		}
		after = items[len(items)-1].name
	}
}

func updateScanStageEntries(ctx context.Context, stage *storage.ScanStore, items []stagedScanItem) error {
	return stage.Write(ctx, func(tx *sql.Tx) (err error) {
		stmt, err := tx.PrepareContext(ctx, "UPDATE snapshot SET entry=? WHERE name=?")
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, stmt.Close()) }()
		for _, item := range items {
			if _, err := stmt.ExecContext(ctx, item.entry, item.name); err != nil {
				return err
			}
		}
		return nil
	})
}

// writeScanGenerationPageOptions groups the inputs for writeScanGenerationPage.
type writeScanGenerationPageOptions struct {
	ID     int64
	Gen    int64
	Marker string
	Items  []stagedScanItem
}

func (l *Library) writeScanGenerationPage(ctx context.Context, params writeScanGenerationPageOptions) error {
	id, gen, marker, items := params.ID, params.Gen, params.Marker, params.Items

	return l.db.Write(ctx, func(tx *sql.Tx) (err error) {
		entryStmt, err := tx.PrepareContext(ctx, "INSERT INTO entries(parent,name,kind,identity,size,mtime_ns,present,observed) VALUES(?,?,?,?,?,?,0,?)")
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, entryStmt.Close()) }()
		itemStmt, err := tx.PrepareContext(ctx, "INSERT INTO directory_items VALUES(?,?,?,?,?,?,?)")
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, itemStmt.Close()) }()
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
		return nil
	})
}

// publishDirectoryScanOptions groups the inputs for publishDirectoryScan.
type publishDirectoryScanOptions struct {
	ID        int64
	Gen       int64
	Marker    string
	Baseline  scanBaseline
	StagePath string
	Rebuild   bool
}

func (l *Library) publishDirectoryScan(ctx context.Context, params publishDirectoryScanOptions) error {
	id, gen, marker, baseline, stagePath, rebuild := params.ID, params.Gen, params.Marker, params.Baseline, params.StagePath, params.Rebuild

	if err := l.lockNumbering(ctx); err != nil {
		return err
	}
	defer l.mu.Unlock()
	if l.broken != nil {
		return errHalted.Wrap(l.broken)
	}
	if err := l.CheckAvailable(ctx, id); err != nil {
		return err
	}
	err := l.db.WriteScan(ctx, stagePath, func(tx *sql.Tx) error {
		var err error
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
		 version=e.version+CASE WHEN e.size<>s.size OR e.mtime_ns<>s.mtime_ns THEN 1 ELSE 0 END,
		 kind=s.kind,size=s.size,mtime_ns=s.mtime_ns,observed=?
		 FROM directory_scan.snapshot s JOIN directory_scan.baseline b ON s.entry=b.id
		 WHERE e.id=s.entry AND (s.kind<>b.kind OR s.size<>b.size OR s.mtime_ns<>b.mtime_ns)`, storage.Now()); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO directory_state(dir,generation,refreshed,error) VALUES(?,?,?,'')
	 ON CONFLICT(dir) DO UPDATE SET generation=excluded.generation,refreshed=excluded.refreshed,error=''`, id, gen, storage.Now()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if rebuild {
		l.indexEpoch.Add(1)
	}
	return nil
}

// cleanScanStagingOptions groups the inputs for cleanScanStaging.
type cleanScanStagingOptions struct {
	Stage     *storage.ScanStore
	ID        int64
	Gen       int64
	Marker    string
	Published bool
}

func (l *Library) cleanScanStaging(ctx context.Context, params cleanScanStagingOptions) error {
	stage, id, gen, marker, published := params.Stage, params.ID, params.Gen, params.Marker, params.Published

	for {
		count, err := l.cleanScanGenerationPage(ctx, cleanScanGenerationPageOptions{ID: id, Gen: gen, Marker: marker, Published: published})
		if err != nil {
			return fmt.Errorf("clean directory %d scan generation: %w", id, err)
		}
		if count == 0 {
			break
		}
	}
	var after int64
	for {
		var orphans []scanOrphan
		if err := stage.View(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, "SELECT entry,observed FROM orphan_candidates WHERE parent=? AND entry>? ORDER BY entry LIMIT ?", id, after, refreshPageSize)
			if err != nil {
				return err
			}
			orphans, err = storage.CollectRows(rows, func(rows *sql.Rows) (scanOrphan, error) {
				var item scanOrphan
				err := rows.Scan(&item.entry, &item.observed)
				return item, err
			})
			return err
		}); err != nil {
			return fmt.Errorf("read directory %d scan orphan candidates: %w", id, err)
		}
		if len(orphans) == 0 {
			return nil
		}
		if err := l.db.Write(ctx, func(tx *sql.Tx) error {
			return deleteCapturedScanOrphans(ctx, deleteCapturedScanOrphansOptions{Tx: tx, ID: id, Marker: marker, Published: published, Orphans: orphans})
		}); err != nil {
			return fmt.Errorf("clean directory %d scan orphan candidates: %w", id, err)
		}
		after = orphans[len(orphans)-1].entry
	}
}

// cleanScanGenerationPageOptions groups the inputs for cleanScanGenerationPage.
type cleanScanGenerationPageOptions struct {
	ID        int64
	Gen       int64
	Marker    string
	Published bool
}

// Removing obsolete generation rows and their invisible candidates in one
// transaction prevents cleanup interruption from creating new orphan entries.
// The indexed generation rows also identify candidates whose page committed
// before a local stage update failed.
func (l *Library) cleanScanGenerationPage(ctx context.Context, params cleanScanGenerationPageOptions) (int, error) {
	id, gen, marker, published := params.ID, params.Gen, params.Marker, params.Published

	comparison := "="
	if published {
		comparison = "<>"
	}
	return storage.WriteValue(ctx, l.db, func(tx *sql.Tx) (_ int, err error) {
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
		err = errors.Join(err, rows.Close())
		if err != nil || len(rowIDs) == 0 {
			return 0, err
		}
		stmt, err := tx.PrepareContext(ctx, "DELETE FROM directory_items WHERE rowid=?")
		if err != nil {
			return 0, err
		}
		defer func() { err = errors.Join(err, stmt.Close()) }()
		for _, row := range rowIDs {
			if _, err = stmt.ExecContext(ctx, row); err != nil {
				return 0, err
			}
		}
		if err = deleteScanCandidates(ctx, deleteScanCandidatesOptions{Tx: tx, ID: id, Marker: marker, Published: published, Entries: entries}); err != nil {
			return 0, err
		}
		return len(rowIDs), nil
	})
}

// deleteScanCandidatesOptions groups the inputs for deleteScanCandidates.
type deleteScanCandidatesOptions struct {
	Tx        *sql.Tx
	ID        int64
	Marker    string
	Published bool
	Entries   []int64
}

func deleteScanCandidates(ctx context.Context, params deleteScanCandidatesOptions) (err error) {
	tx, id, marker, published, entries := params.Tx, params.ID, params.Marker, params.Published, params.Entries

	filter, value := "observed=?", marker
	if published {
		filter, value = "observed LIKE ?", "directory-scan:%"
	}
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM entries WHERE id=? AND parent=? AND present=0 AND "+filter+" AND NOT EXISTS(SELECT 1 FROM directory_items i WHERE i.dir=entries.parent AND i.entry=entries.id)")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stmt.Close()) }()
	for _, entry := range entries {
		if _, err = stmt.ExecContext(ctx, entry, id, value); err != nil {
			return err
		}
	}
	return nil
}

// deleteCapturedScanOrphansOptions groups the inputs for deleteCapturedScanOrphans.
type deleteCapturedScanOrphansOptions struct {
	Tx        *sql.Tx
	ID        int64
	Marker    string
	Published bool
	Orphans   []scanOrphan
}

func deleteCapturedScanOrphans(ctx context.Context, params deleteCapturedScanOrphansOptions) (err error) {
	tx, id, marker, published, orphans := params.Tx, params.ID, params.Marker, params.Published, params.Orphans

	stmt, err := tx.PrepareContext(ctx, `DELETE FROM entries WHERE id=? AND parent=? AND present=0 AND observed=?
	 AND NOT EXISTS(SELECT 1 FROM directory_items i WHERE i.dir=entries.parent AND i.entry=entries.id)`)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stmt.Close()) }()
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
