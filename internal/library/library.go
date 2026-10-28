// Package library maps real media entries and coordinates all filesystem mutations.
package library

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/pagination"
	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

// Entry is one indexed file or directory.
type Entry struct {
	Progress   *DirectoryProgress `json:"progress,omitzero"`
	Contents   *DirectoryContents `json:"contents,omitzero"`
	TaskStatus string             `json:"task_status"`
	ID         int64              `json:"id"`
	Parent     int64              `json:"parent"`
	Name       string             `json:"name"`
	Kind       string             `json:"kind"`
	Identity   string             `json:"-"`
	Size       int64              `json:"size_bytes"`
	RenamedAt  string             `json:"renamed_at"`
	ModifiedNS int64              `json:"-"`
	Version    int64              `json:"version"`
	Processed  string             `json:"processed"`
	RestoreID  int64              `json:"restore_id"`
}

// Library coordinates filesystem changes. mu protects mutations and recovery;
// refreshMu protects refresh/scanner lifecycles, and progressCacheMu protects
// cached counts. indexEpoch is an atomic hint, never file identity evidence.
type Library struct {
	logger          *slog.Logger
	progressCacheMu sync.Mutex
	progressCache   map[int64]directDirectoryCache
	db              *storage.Store
	Root            string
	mu              sync.Mutex
	refreshMu       sync.Mutex
	refreshing      map[int64]context.CancelFunc
	scanning        map[int64]bool
	dirScans        map[int64]*directoryScanLock
	indexEpoch      atomic.Uint64
	scanSlots       chan struct{}
	Busy            func(context.Context, int64) bool
	broken          error
	refreshWG       sync.WaitGroup
	closed          bool
}

// Identity returns the device and inode identity of s.
func Identity(s domain.Snapshot) string { return fmt.Sprintf("%d:%d", s.Dev, s.Ino) }
func stamp(s domain.Snapshot) int64     { return s.ModTime.UnixNano() }

// New opens the library rooted at root.
func New(ctx context.Context, db *storage.Store, root string, logger *slog.Logger) (*Library, error) {
	if logger == nil {
		return nil, errors.New("library logger is required")
	}
	snap, err := domain.StatSnapshot(root)
	if err != nil {
		return nil, err
	}
	var old, ident string
	err = db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT root,identity FROM library WHERE id=1").Scan(&old, &ident)
	})
	if err == nil && (old != root || ident != Identity(snap)) {
		return nil, errors.New("media root bound to the database changed; check the mount and configuration")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err = storage.WriteValue(ctx, db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "INSERT OR IGNORE INTO library VALUES(1,?,?)", root, Identity(snap))
	}); err != nil {
		return nil, err
	}
	if _, err = storage.WriteValue(ctx, db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "INSERT OR IGNORE INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(1,0,'','directory',?,?,?,?)", Identity(snap), snap.Size, stamp(snap), storage.Now())
	}); err != nil {
		return nil, err
	}
	l := &Library{db: db, Root: root, logger: logger.With("component", "library"), refreshing: map[int64]context.CancelFunc{}, scanSlots: make(chan struct{}, 2)}
	if err = l.recoverNumberings(ctx); err != nil {
		return nil, err
	}
	if err = l.Recover(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

// Get returns entry id.
func (l *Library) Get(ctx context.Context, id int64) (Entry, error) {
	entries, err := l.GetBatch(ctx, []int64{id})
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, sql.ErrNoRows
	}
	return entries[0], nil
}

// GetBatch reads live status in one snapshot and preserves the requested order.
func (l *Library) GetBatch(ctx context.Context, ids []int64) ([]Entry, error) {
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) ([]Entry, error) { return getEntries(ctx, tx, ids) })
}

func getEntry(ctx context.Context, tx *sql.Tx, id int64) (Entry, error) {
	entries, err := getEntries(ctx, tx, []int64{id})
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, sql.ErrNoRows
	}
	return entries[0], nil
}

func getEntries(ctx context.Context, tx *sql.Tx, ids []int64) (entries []Entry, err error) {
	out := []Entry{}
	if len(ids) == 0 {
		return out, ctx.Err()
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime_ns,e.version,
	 `+entryProcessedSQL+`,
 COALESCE((SELECT id FROM operations o WHERE o.entry=e.id AND o.status='committed' AND o.undone=0 AND o.undo_of=0 ORDER BY id DESC LIMIT 1),0)
	 ,`+entryTaskStatusSQL+`
,COALESCE((SELECT created FROM operations o WHERE o.entry=e.id AND o.status='committed' ORDER BY id DESC LIMIT 1),'')
 FROM entries e WHERE e.id IN (SELECT value FROM json_each(?)) AND e.present=1`, string(raw))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	found := make(map[int64]Entry, len(ids))
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.ModifiedNS, &e.Version, &e.Processed, &e.RestoreID, &e.TaskStatus, &e.RenamedAt); err != nil {
			return nil, err
		}
		found[e.ID] = e
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if e, ok := found[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// IndexedEntry loads only the indexed file snapshot and its processed mark.
// Workers and path validation do not need UI history or running-scope summaries.
func (l *Library) IndexedEntry(ctx context.Context, id int64) (Entry, error) {
	var e Entry
	err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime_ns,e.version,
 COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime_ns=e.mtime_ns),'')
 FROM entries e WHERE e.id=? AND e.present=1`, id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.ModifiedNS, &e.Version, &e.Processed)
	})
	return e, err
}

// indexedSnapshot omits processing state when only filesystem evidence is needed.
func (l *Library) indexedSnapshot(ctx context.Context, id int64) (Entry, error) {
	var e Entry
	err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,parent,name,kind,identity,size,mtime_ns,version
 FROM entries WHERE id=? AND present=1`, id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.ModifiedNS, &e.Version)
	})
	return e, err
}

// Path checks every ancestor, including mount identity; symlinks are never followed.
func (l *Library) Path(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", errBadHierarchy
	}
	e, err := l.indexedSnapshot(ctx, id)
	if err != nil {
		return "", err
	}
	return l.pathForEntry(ctx, e)
}

// pathForEntry reuses only the caller's freshly read entry. Every ancestor is
// still loaded and checked against disk on this call; no path evidence is cached.
func (l *Library) pathForEntry(ctx context.Context, e Entry) (string, error) {
	chain := []Entry{}
	seen := map[int64]bool{}
	for e.ID != 1 {
		if e.ID <= 0 || seen[e.ID] || len(chain) > 1024 {
			return "", errBadHierarchy
		}
		seen[e.ID] = true
		if e.Name == "" || filepath.Base(e.Name) != e.Name || strings.ContainsAny(e.Name, "/\\") {
			return "", errIllegalPath
		}
		chain = append(chain, e)
		if e.Parent <= 0 {
			return "", errBadHierarchy
		}
		var err error
		e, err = l.indexedSnapshot(ctx, e.Parent)
		if err != nil {
			return "", err
		}
	}
	rs, err := domain.StatSnapshot(l.Root)
	if err != nil || Identity(rs) != e.Identity {
		return "", errRootUnavailable
	}
	p := l.Root
	for _, entry := range slices.Backward(chain) {
		p = filepath.Join(p, entry.Name)
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", errSymlink
		}
		s, err := domain.SnapshotFromInfo(st)
		if err != nil {
			return "", err
		}
		if Identity(s) != entry.Identity {
			var before domain.Snapshot
			_, parseErr := fmt.Sscanf(entry.Identity, "%d:%d", &before.Dev, &before.Ino)
			before.Size = entry.Size
			before.ModTime = time.Unix(0, entry.ModifiedNS)
			diagnostic := domain.SourceChangedError(domain.SourceChange{Message: "file identity changed; refresh the directory", Stage: domain.StagePathIdentity, Path: p, Before: before, After: s})
			diagnostic.ProcessingLog += fmt.Sprintf("entry: %d\nkind: %s\nindexed identity: %q\nidentity parse error: %v\nsize or mtime_ns changes of a directory alone do not cause this identity error.\n", entry.ID, entry.Kind, entry.Identity, parseErr)
			return "", diagnostic
		}
	}
	return p, nil
}

// Relative returns the path of entry id relative to the library root.
func (l *Library) Relative(ctx context.Context, id int64) string {
	p, e := l.Path(ctx, id)
	if e != nil {
		return ""
	}
	// Path returned an absolute descendant of this absolute root.
	r, _ := filepath.Rel(l.Root, p)
	return r
}
func kindOf(st os.FileInfo) string {
	if st.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if st.IsDir() {
		return "directory"
	}
	if st.Mode().IsRegular() {
		return "file"
	}
	return "other"
}

// StartRefresh rescans directory id in the background unless it is already
// being refreshed or scanned. The refresh is detached from ctx cancellation so
// it outlives the request that noticed the stale index; Close cancels it.
func (l *Library) StartRefresh(ctx context.Context, id int64) {
	l.refreshMu.Lock()
	defer l.refreshMu.Unlock()
	if l.refreshing[id] != nil || l.scanning[id] || l.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	l.refreshing[id] = cancel
	l.refreshWG.Go(func() {
		defer func() {
			l.refreshMu.Lock()
			delete(l.refreshing, id)
			l.refreshMu.Unlock()
			cancel()
		}()
		select {
		case l.scanSlots <- struct{}{}:
			defer func() { <-l.scanSlots }()
		case <-ctx.Done():
			return
		}
		if err := l.Refresh(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
			l.logger.Error("directory_refresh_failed", "directory_id", id, "error", err)
		}
	})
}

// Page is a page of directory entries.
type Page struct {
	Total       int           `json:"total"`
	Offset      int           `json:"offset"`
	Limit       int           `json:"limit"`
	Items       []Entry       `json:"items"`
	Generation  int64         `json:"generation,string"`
	Refreshing  bool          `json:"refreshing"`
	Refreshed   string        `json:"refreshed"`
	Error       domain.Record `json:"error,omitzero"`
	Directory   Entry         `json:"directory"`
	Breadcrumbs []Entry       `json:"breadcrumbs"`
}

// ListPageOptions groups the inputs for ListPage.
type ListPageOptions struct {
	ID     int64
	Offset int
	Limit  int
	Query  string
	Kind   string
	Status string
	SortBy string
}

// ListPage reads a row window from the current directory index.
// Count and page boundaries come from one snapshot, even during a rescan.
func (l *Library) ListPage(ctx context.Context, params ListPageOptions) (Page, error) {
	id, offset, limit, q, kind, status, sortBy := params.ID, max(0, params.Offset), params.Limit, params.Query, params.Kind, params.Status, params.SortBy
	if !slices.Contains([]string{"", "file", "directory", "video", "audio", "image", "other"}, kind) {
		return Page{}, domain.InvalidField("kind", "invalid_choice")
	}
	if sortBy != "" && !slices.Contains([]string{"name_asc", "name_desc", "size_asc", "size_desc", "mtime_asc", "mtime_desc", "renamed_asc", "renamed_desc", "status_asc", "status_desc"}, sortBy) {
		return Page{}, domain.InvalidField("sort", "invalid_choice")
	}

	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) (Page, error) {
		p := Page{Items: []Entry{}, Breadcrumbs: []Entry{}}
		statusFilter, err := processingStatusFilter(status)
		if err != nil {
			return p, err
		}
		p.Directory, err = getEntry(ctx, tx, id)
		if err != nil {
			return p, err
		}
		if p.Directory.Kind != "directory" {
			return p, errNotDirectory
		}
		var refreshed sql.NullString
		err = tx.QueryRowContext(ctx, "SELECT generation,refreshed,error FROM directory_state WHERE dir=?", id).Scan(&p.Generation, &refreshed, &p.Error)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return p, err
		}
		p.Refreshed = refreshed.String
		// Missing or malformed freshness evidence deliberately forces a refresh.
		at, _ := time.Parse(timefmt.Layout, p.Refreshed)
		if time.Since(at) > 30*time.Second {
			l.StartRefresh(ctx, id)
		}
		l.refreshMu.Lock()
		p.Refreshing = l.refreshing[id] != nil || l.scanning[id]
		l.refreshMu.Unlock()
		if limit != 50 && limit != 100 && limit != 200 {
			limit = 100
		}
		value, sortBy := directoryOrder(sortBy)
		queryPrefix := ""
		queryArgs := []any{}
		if sortBy == "status_asc" || sortBy == "status_desc" {
			ranks, err := directoryStatusRanks(ctx, directoryStatusRanksOptions{Tx: tx, ID: id, Generation: p.Generation, Query: q})
			if err != nil {
				return p, err
			}
			queryPrefix = `WITH directory_status_ranks AS MATERIALIZED (SELECT CAST(key AS INTEGER) AS status_entry,value AS rank FROM json_each(?)) `
			queryArgs = append(queryArgs, ranks)
			value = `CASE WHEN kind='directory' THEN COALESCE((SELECT rank FROM directory_status_ranks WHERE status_entry=directory_items.entry),0) ELSE ` + value + ` END`
		}
		filter := "(?='' OR kind=?)"
		if kind == "video" || kind == "audio" || kind == "other" || kind == "image" {
			videoSQL := `(lower(name) LIKE '%.mp4' OR lower(name) LIKE '%.m4v' OR lower(name) LIKE '%.mkv' OR lower(name) LIKE '%.mov' OR lower(name) LIKE '%.avi' OR lower(name) LIKE '%.webm' OR lower(name) LIKE '%.flv' OR lower(name) LIKE '%.wmv' OR lower(name) LIKE '%.mpg' OR lower(name) LIKE '%.mpeg' OR lower(name) LIKE '%.ts' OR lower(name) LIKE '%.m2ts' OR lower(name) LIKE '%.mts' OR lower(name) LIKE '%.3gp' OR lower(name) LIKE '%.ogv' OR lower(name) LIKE '%.rmvb' OR lower(name) LIKE '%.asf' OR lower(name) LIKE '%.vob')`
			imageSQL := `(lower(name) LIKE '%.jpg' OR lower(name) LIKE '%.jpeg' OR lower(name) LIKE '%.png' OR lower(name) LIKE '%.gif' OR lower(name) LIKE '%.webp' OR lower(name) LIKE '%.avif' OR lower(name) LIKE '%.bmp')`
			audioSQL := `(lower(name) LIKE '%.mp3' OR lower(name) LIKE '%.m4a' OR lower(name) LIKE '%.m4b' OR lower(name) LIKE '%.aac' OR lower(name) LIKE '%.flac' OR lower(name) LIKE '%.wav' OR lower(name) LIKE '%.wave' OR lower(name) LIKE '%.ogg' OR lower(name) LIKE '%.oga' OR lower(name) LIKE '%.opus' OR lower(name) LIKE '%.wma' OR lower(name) LIKE '%.aiff' OR lower(name) LIKE '%.aif' OR lower(name) LIKE '%.caf')`
			mediaSQL := videoSQL
			if kind == "image" {
				mediaSQL = imageSQL
			}
			if kind == "audio" {
				mediaSQL = audioSQL
			}
			if kind == "other" {
				mediaSQL = "NOT (" + videoSQL + " OR " + imageSQL + " OR " + audioSQL + ")"
			}
			filter = "(?<>'' AND ?<>'' AND kind='file' AND " + mediaSQL + ")"
			if kind != "other" {
				filter = "(" + filter + " OR kind='directory')"
			}
		}
		filterArgs := []any{kind, kind}
		if status != "" {
			directoryIDs := "[]"
			if kind == "" || kind == "directory" || kind == "video" || kind == "image" || kind == "audio" {
				directoryIDs, err = directoryStatusIDs(ctx, directoryStatusIDsOptions{Tx: tx, ID: id, Generation: p.Generation, Query: q, Status: status})
				if err != nil {
					return p, err
				}
			}
			filter = "(" + filter + ") AND ((kind='directory' AND entry IN (SELECT value FROM json_each(?))) OR (kind<>'directory' AND " + statusFilter + "))"
			filterArgs = append(filterArgs, directoryIDs)
		}
		countArgs := append([]any{id, p.Generation, q}, filterArgs...)
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM directory_items WHERE dir=? AND generation=? AND instr(lower(name),lower(?))>0 AND `+filter, countArgs...).Scan(&p.Total); err != nil {
			return p, err
		}
		order := "ASC"
		if strings.HasSuffix(sortBy, "_desc") {
			order = "DESC"
		}
		p.Offset = pagination.Offset(offset, p.Total, limit)
		p.Limit = limit
		pageArgs := append(append([]any{}, queryArgs...), id, p.Generation, q)
		pageArgs = append(pageArgs, filterArgs...)
		pageArgs = append(pageArgs, limit, p.Offset)
		rows, err := tx.QueryContext(ctx, queryPrefix+`SELECT entry FROM directory_items WHERE dir=? AND generation=? AND instr(lower(name),lower(?))>0 AND `+filter+` ORDER BY CASE WHEN kind='directory' THEN 0 ELSE 1 END,`+value+` `+order+`,name COLLATE NATURAL_NAME `+order+`,entry `+order+` LIMIT ? OFFSET ?`, pageArgs...)
		if err != nil {
			return p, err
		}
		ids, err := storage.CollectRows(rows, func(rows *sql.Rows) (int64, error) {
			var eid int64
			err := rows.Scan(&eid)
			return eid, err
		})
		if err != nil {
			return p, err
		}
		// Entries and breadcrumbs use the same snapshot as the page boundaries.

		p.Items, err = getEntries(ctx, tx, ids)
		if err != nil {
			return p, err
		}
		for cur := id; cur > 0 && len(p.Breadcrumbs) <= 1024; {
			e, er := getEntry(ctx, tx, cur)
			if er != nil {
				if ctx.Err() != nil {
					return p, ctx.Err()
				}
				break
			}
			p.Breadcrumbs = append([]Entry{e}, p.Breadcrumbs...)
			cur = e.Parent
		}
		return p, nil
	})
}

// Close cancels background refreshes and waits for them to finish.
func (l *Library) Close() {
	l.refreshMu.Lock()
	l.closed = true
	for _, cancel := range l.refreshing {
		cancel()
	}
	l.refreshMu.Unlock()
	l.refreshWG.Wait()
}

// DisplayPath uses the cached hierarchy only; callers must use Path before mutation.
// Failure records pass a context without cancellation so cancelled work still
// names its file.
func (l *Library) DisplayPath(ctx context.Context, id int64) string {
	parts := []string{}
	for i := 0; id > 1 && i < 1024; i++ {
		var parent int64
		var name string
		if err := l.db.View(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT parent,name FROM entries WHERE id=?", id).Scan(&parent, &name)
		}); err != nil {
			return MissingDisplayPath
		}
		parts = append([]string{name}, parts...)
		id = parent
	}
	if len(parts) == 0 {
		return RootDisplayPath
	}
	return strings.Join(parts, " / ")
}

func directoryOrder(sortBy string) (string, string) {
	value := "name COLLATE NATURAL_NAME"
	switch sortBy {
	case "size_asc", "size_desc":
		value = "CASE WHEN kind='directory' THEN 0 ELSE size END"
	case "mtime_asc", "mtime_desc":
		value = "mtime_ns"
	case "renamed_asc", "renamed_desc":
		value = "COALESCE((SELECT created FROM operations o WHERE o.entry=directory_items.entry AND o.status='committed' ORDER BY id DESC LIMIT 1),'')"
	case "status_asc", "status_desc":
		value = directoryStatusOrderSQL
	case "name_asc", "name_desc":
	default:
		sortBy = "name_asc"
	}
	return value, sortBy
}
