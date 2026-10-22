// Package library maps real media entries and coordinates all filesystem mutations.
package library

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

type Entry struct {
	Progress   *DirectoryProgress `json:"progress,omitempty"`
	Contents   *DirectoryContents `json:"contents"`
	TaskStatus string             `json:"task_status"`
	ID         int64              `json:"id"`
	Parent     int64              `json:"parent"`
	Name       string             `json:"name"`
	Kind       string             `json:"kind"`
	Identity   string             `json:"-"`
	Size       int64              `json:"size"`
	RenamedAt  string             `json:"renamed_at"`
	Mtime      string             `json:"mtime"`
	Version    int64              `json:"version"`
	Processed  string             `json:"processed"`
	RestoreID  int64              `json:"restore_id"`
}
type Library struct {
	progressCacheMu sync.Mutex
	progressCache   map[int64]directDirectoryCache
	DB              *storage.Store
	Root            string
	mu              sync.Mutex
	refreshMu       sync.Mutex
	refreshing      map[int64]bool
	scanning        map[int64]bool
	dirScans        map[int64]*directoryScanLock
	indexEpoch      atomic.Uint64
	scanSlots       chan struct{}
	Busy            func(int64) bool
	broken          error
	refreshWG       sync.WaitGroup
	closed          bool
	RefreshContext  context.Context
}

func Identity(s domain.Snapshot) string { return fmt.Sprintf("%d:%d", s.Dev, s.Ino) }
func stamp(s domain.Snapshot) string    { return s.ModTime.UTC().Format(time.RFC3339Nano) }
func New(db *storage.Store, root string) (*Library, error) {
	snap, err := domain.StatSnapshot(root)
	if err != nil {
		return nil, err
	}
	var old, ident string
	err = db.QueryRow("SELECT root,identity FROM library WHERE id=1").Scan(&old, &ident)
	if err == nil && (old != root || ident != Identity(snap)) {
		return nil, fmt.Errorf("数据库绑定的媒体根目录已变化，请核对挂载与配置")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err = db.Exec("INSERT OR IGNORE INTO library VALUES(1,?,?)", root, Identity(snap)); err != nil {
		return nil, err
	}
	if _, err = db.Exec("INSERT OR IGNORE INTO entries(id,parent,name,kind,identity,size,mtime,observed) VALUES(1,0,'','directory',?,?,?,?)", Identity(snap), snap.Size, stamp(snap), storage.Now()); err != nil {
		return nil, err
	}
	l := &Library{DB: db, Root: root, refreshing: map[int64]bool{}, scanSlots: make(chan struct{}, 2)}
	if err = l.recoverNumberings(); err != nil {
		return nil, err
	}
	if err = l.Recover(); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *Library) Get(id int64) (Entry, error) {
	return l.GetContext(context.Background(), id)
}

func (l *Library) GetContext(ctx context.Context, id int64) (Entry, error) {
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
	out := []Entry{}
	if len(ids) == 0 {
		return out, ctx.Err()
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := l.DB.Read().QueryContext(ctx, `SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime,e.version,
	 `+entryProcessedSQL+`,
 COALESCE((SELECT id FROM operations o WHERE o.entry=e.id AND o.status='committed' AND o.undone=0 AND o.undo_of=0 ORDER BY id DESC LIMIT 1),0)
	 ,`+entryTaskStatusSQL+`
,COALESCE((SELECT created FROM operations o WHERE o.entry=e.id AND o.status='committed' ORDER BY id DESC LIMIT 1),'')
 FROM entries e WHERE e.id IN (SELECT value FROM json_each(?)) AND e.present=1`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make(map[int64]Entry, len(ids))
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.Mtime, &e.Version, &e.Processed, &e.RestoreID, &e.TaskStatus, &e.RenamedAt); err != nil {
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
func (l *Library) IndexedEntry(id int64) (Entry, error) {
	var e Entry
	err := l.DB.Read().QueryRow(`SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime,e.version,
 COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),'')
 FROM entries e WHERE e.id=? AND e.present=1`, id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.Mtime, &e.Version, &e.Processed)
	return e, err
}

// indexedSnapshot omits processing state when only filesystem evidence is needed.
func (l *Library) indexedSnapshot(id int64) (Entry, error) {
	var e Entry
	err := l.DB.Read().QueryRow(`SELECT id,parent,name,kind,identity,size,mtime,version
 FROM entries WHERE id=? AND present=1`, id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.Mtime, &e.Version)
	return e, err
}

// Path checks every ancestor, including mount identity; symlinks are never followed.
func (l *Library) Path(id int64) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("目录关系无效")
	}
	e, err := l.indexedSnapshot(id)
	if err != nil {
		return "", err
	}
	return l.pathForEntry(e)
}

// pathForEntry reuses only the caller's freshly read entry. Every ancestor is
// still loaded and checked against disk on this call; no path evidence is cached.
func (l *Library) pathForEntry(e Entry) (string, error) {
	chain := []Entry{}
	seen := map[int64]bool{}
	for e.ID != 1 {
		if e.ID <= 0 || seen[e.ID] || len(chain) > 1024 {
			return "", fmt.Errorf("目录关系无效")
		}
		seen[e.ID] = true
		if e.Name == "" || filepath.Base(e.Name) != e.Name || strings.ContainsAny(e.Name, "/\\") {
			return "", fmt.Errorf("非法路径")
		}
		chain = append(chain, e)
		if e.Parent <= 0 {
			return "", fmt.Errorf("目录关系无效")
		}
		var err error
		e, err = l.indexedSnapshot(e.Parent)
		if err != nil {
			return "", err
		}
	}
	rs, err := domain.StatSnapshot(l.Root)
	if err != nil || Identity(rs) != e.Identity {
		return "", fmt.Errorf("媒体根目录不可用或挂载已变化")
	}
	p := l.Root
	for _, entry := range slices.Backward(chain) {
		p = filepath.Join(p, entry.Name)
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("不允许操作符号链接")
		}
		s, err := domain.SnapshotFromInfo(st)
		if err != nil {
			return "", err
		}
		if Identity(s) != entry.Identity {
			var before domain.Snapshot
			_, parseErr := fmt.Sscanf(entry.Identity, "%d:%d", &before.Dev, &before.Ino)
			before.Size = entry.Size
			before.ModTime, _ = time.Parse(time.RFC3339Nano, entry.Mtime)
			diagnostic := domain.SourceChangedError("文件身份已变化，请刷新目录", "路径及祖先目录身份校验（仅设备号与 inode 参与判定）", p, before, s)
			diagnostic.ProcessingLog += fmt.Sprintf("条目：%d\n类型：%s\n索引身份：%q\n索引身份解析错误：%v\n目录大小或修改时间变化本身不会触发此次身份错误。\n", entry.ID, entry.Kind, entry.Identity, parseErr)
			return "", diagnostic
		}
	}
	return p, nil
}
func (l *Library) Relative(id int64) string {
	p, e := l.Path(id)
	if e != nil {
		return ""
	}
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
func (l *Library) StartRefresh(ctx context.Context, id int64) {
	l.refreshMu.Lock()
	if l.refreshing[id] || l.scanning[id] || l.closed || ctx.Err() != nil {
		l.refreshMu.Unlock()
		return
	}
	l.refreshing[id] = true
	l.refreshWG.Go(func() {
		defer func() { l.refreshMu.Lock(); delete(l.refreshing, id); l.refreshMu.Unlock() }()
		select {
		case l.scanSlots <- struct{}{}:
			defer func() { <-l.scanSlots }()
		case <-ctx.Done():
			return
		}
		_ = l.Refresh(ctx, id)
	})
	l.refreshMu.Unlock()
}

type Page struct {
	Total       int     `json:"total"`
	Number      int     `json:"page,omitzero"`
	Items       []Entry `json:"items"`
	Next        int     `json:"next"`
	Generation  int64   `json:"generation,string"`
	Refreshing  bool    `json:"refreshing"`
	Refreshed   string  `json:"refreshed"`
	Error       string  `json:"error"`
	Directory   Entry   `json:"directory"`
	Breadcrumbs []Entry `json:"breadcrumbs"`
}

// ListOrdered continues a cursor listing; a cursor from an older generation is rejected.
func (l *Library) ListOrdered(ctx context.Context, id int64, offset, limit int, generation int64, q, kind, status string, force bool, sortBy string) (Page, error) {
	return l.listOrdered(ctx, id, offset, limit, generation, q, kind, status, force, sortBy, 0)
}

// ListPage reads a fresh page without carrying a cursor from an older index.
// Count and page boundaries come from one snapshot, even during a rescan.
func (l *Library) ListPage(ctx context.Context, id int64, page, limit int, q, kind, status, sortBy string) (Page, error) {
	return l.listOrdered(ctx, id, 0, limit, 0, q, kind, status, false, sortBy, max(1, page))
}

func (l *Library) listOrdered(ctx context.Context, id int64, offset, limit int, generation int64, q, kind, status string, force bool, sortBy string, pageNumber int) (Page, error) {
	p := Page{Items: []Entry{}, Breadcrumbs: []Entry{}}
	statusFilter, err := processingStatusFilter(status)
	if err != nil {
		return p, err
	}
	p.Directory, err = l.GetContext(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Directory.Kind != "directory" {
		return p, fmt.Errorf("不是目录")
	}
	tx, err := l.DB.Read().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var refreshed sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT generation,refreshed,error FROM directory_state WHERE dir=?", id).Scan(&p.Generation, &refreshed, &p.Error)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	p.Refreshed = refreshed.String
	at, _ := time.Parse(time.RFC3339Nano, p.Refreshed)
	if force || time.Since(at) > 30*time.Second {
		refreshCtx := l.RefreshContext
		if refreshCtx == nil {
			refreshCtx = ctx
		}
		l.StartRefresh(refreshCtx, id)
	}
	l.refreshMu.Lock()
	p.Refreshing = l.refreshing[id] || l.scanning[id]
	l.refreshMu.Unlock()
	if generation != 0 && p.Generation != generation {
		return p, fmt.Errorf("目录已刷新，请返回第一页")
	}
	if limit != 50 && limit != 100 && limit != 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var anchorKind int
	var anchorName string
	var anchorValue any
	var anchorID int64
	value, sortBy := directoryOrder(sortBy)
	queryPrefix := ""
	queryArgs := []any{}
	if sortBy == "status_asc" || sortBy == "status_desc" {
		ranks, err := directoryStatusRanks(ctx, tx, id, p.Generation, q)
		if err != nil {
			return p, err
		}
		queryPrefix = `WITH directory_status_ranks AS MATERIALIZED (SELECT CAST(key AS INTEGER) AS status_entry,value AS rank FROM json_each(?)) `
		queryArgs = append(queryArgs, ranks)
		value = `CASE WHEN kind='directory' THEN COALESCE((SELECT rank FROM directory_status_ranks WHERE status_entry=directory_items.entry),0) ELSE ` + value + ` END`
	}
	if offset > 0 {
		if err = tx.QueryRowContext(ctx, queryPrefix+"SELECT CASE WHEN kind='directory' THEN 0 ELSE 1 END,name,entry,"+value+" FROM directory_items WHERE dir=? AND generation=? AND entry=?", append(append([]any{}, queryArgs...), id, p.Generation, offset)...).Scan(&anchorKind, &anchorName, &anchorID, &anchorValue); err != nil {
			return p, fmt.Errorf("分页位置已失效，请返回第一页")
		}
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
			directoryIDs, err = directoryStatusIDs(ctx, tx, id, p.Generation, q, status)
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
	order, comparison := "ASC", ">"
	if strings.HasSuffix(sortBy, "_desc") {
		order, comparison = "DESC", "<"
	}
	skip := 0
	if pageNumber > 0 {
		// Clamp before multiplying so arbitrary client page numbers cannot overflow.
		p.Number = min(pageNumber, max(1, (p.Total+limit-1)/limit))
		skip = (p.Number - 1) * limit
	}
	pageArgs := append(append([]any{}, queryArgs...), id, p.Generation, q)
	pageArgs = append(pageArgs, filterArgs...)
	pageArgs = append(pageArgs, offset, anchorKind, anchorKind, anchorValue, anchorName, anchorID, limit+1, skip)
	rows, err := tx.QueryContext(ctx, queryPrefix+`SELECT entry FROM directory_items WHERE dir=? AND generation=? AND instr(lower(name),lower(?))>0 AND `+filter+` AND (?=0 OR CASE WHEN kind='directory' THEN 0 ELSE 1 END>? OR (CASE WHEN kind='directory' THEN 0 ELSE 1 END=? AND (`+value+`,name COLLATE NATURAL_NAME,entry)`+comparison+`(?,?,?))) ORDER BY CASE WHEN kind='directory' THEN 0 ELSE 1 END,`+value+` `+order+`,name COLLATE NATURAL_NAME `+order+`,entry `+order+` LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return p, err
	}
	ids := []int64{}
	for rows.Next() {
		var eid int64
		if err = rows.Scan(&eid); err != nil {
			rows.Close()
			return p, err
		}
		ids = append(ids, eid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	// Release the snapshot before Get uses the read pool for live entry status.
	if err = tx.Commit(); err != nil {
		return p, err
	}
	if len(ids) > limit {
		p.Next = int(ids[limit-1])
		ids = ids[:limit]
	}
	p.Items, err = l.GetBatch(ctx, ids)
	if err != nil {
		return p, err
	}
	for cur := id; cur > 0 && len(p.Breadcrumbs) <= 1024; {
		e, er := l.GetContext(ctx, cur)
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
}

func (l *Library) Close() {
	l.refreshMu.Lock()
	l.closed = true
	l.refreshMu.Unlock()
	l.refreshWG.Wait()
}

// DisplayPath uses the cached hierarchy only; callers must use Path before mutation.
func (l *Library) DisplayPath(id int64) string {
	parts := []string{}
	for i := 0; id > 1 && i < 1024; i++ {
		var parent int64
		var name string
		if err := l.DB.Read().QueryRow("SELECT parent,name FROM entries WHERE id=?", id).Scan(&parent, &name); err != nil {
			return "目录已缺失"
		}
		parts = append([]string{name}, parts...)
		id = parent
	}
	if len(parts) == 0 {
		return "整个媒体库"
	}
	return strings.Join(parts, " / ")
}

func directoryOrder(sortBy string) (string, string) {
	value := "name COLLATE NATURAL_NAME"
	switch sortBy {
	case "size_asc", "size_desc":
		value = "CASE WHEN kind='directory' THEN 0 ELSE size END"
	case "mtime_asc", "mtime_desc":
		value = "julianday(mtime)"
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
