// Package library maps real media entries and coordinates all filesystem mutations.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	DB         *storage.Store
	Root       string
	mu         sync.Mutex
	refreshMu  sync.Mutex
	refreshing map[int64]bool
	scanSlots  chan struct{}
	Busy       func(int64) bool
	broken     error
	refreshWG  sync.WaitGroup
	closed     bool
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
	var e Entry
	err := l.DB.QueryRow(`SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime,e.version,
 COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),''),
 COALESCE((SELECT id FROM operations o WHERE o.entry=e.id AND o.status='committed' AND o.undone=0 AND o.undo_of=0 ORDER BY id DESC LIMIT 1),0)
 ,COALESCE((SELECT i.status FROM run_items i JOIN runs r ON r.id=i.run WHERE i.entry=e.id AND i.status IN ('queued','running') AND r.status IN ('queued','scanning','running','cancelling') ORDER BY i.id DESC LIMIT 1),
 (WITH RECURSIVE ancestors(id,parent,depth) AS (
 SELECT e.id,e.parent,0 UNION ALL SELECT p.id,p.parent,a.depth+1 FROM entries p JOIN ancestors a ON p.id=a.parent WHERE a.depth<1024
 ) SELECT CASE WHEN e.kind='directory' THEN r.status ELSE 'queued' END
 FROM runs r JOIN json_each(r.targets) target JOIN entries t ON t.id=target.value
 WHERE r.status IN ('queued','scanning','running','cancelling')
 AND (t.id=e.id OR (t.kind='file' AND e.kind='file' AND t.identity=e.identity) OR (t.kind='directory' AND EXISTS(SELECT 1 FROM ancestors a WHERE a.id=t.id AND (r.recursive=1 OR (a.depth=1 AND e.kind='file')))))
 AND (e.kind='directory' OR NOT EXISTS(SELECT 1 FROM run_items i WHERE i.run=r.id AND i.identity=e.identity))
 ORDER BY r.id LIMIT 1),'')
,COALESCE((SELECT created FROM operations o WHERE o.entry=e.id AND o.status='committed' ORDER BY id DESC LIMIT 1),'')
 FROM entries e WHERE e.id=? AND e.present=1`, id).Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.Mtime, &e.Version, &e.Processed, &e.RestoreID, &e.TaskStatus, &e.RenamedAt)
	return e, err
}

// Path checks every ancestor, including mount identity; symlinks are never followed.
func (l *Library) Path(id int64) (string, error) {
	chain := []Entry{}
	seen := map[int64]bool{}
	for id != 1 {
		if id <= 0 || seen[id] || len(chain) > 1024 {
			return "", fmt.Errorf("目录关系无效")
		}
		seen[id] = true
		e, err := l.Get(id)
		if err != nil {
			return "", err
		}
		if e.Name == "" || filepath.Base(e.Name) != e.Name || strings.ContainsAny(e.Name, "/\\") {
			return "", fmt.Errorf("非法路径")
		}
		chain = append(chain, e)
		id = e.Parent
	}
	root, err := l.Get(1)
	if err != nil {
		return "", err
	}
	rs, err := domain.StatSnapshot(l.Root)
	if err != nil || Identity(rs) != root.Identity {
		return "", fmt.Errorf("媒体根目录不可用或挂载已变化")
	}
	p := l.Root
	for i := len(chain) - 1; i >= 0; i-- {
		p = filepath.Join(p, chain[i].Name)
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("不允许操作符号链接")
		}
		s, err := domain.StatSnapshot(p)
		if err != nil {
			return "", err
		}
		if Identity(s) != chain[i].Identity {
			return "", fmt.Errorf("文件身份已变化，请刷新目录")
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
func (l *Library) IsWithin(id, ancestor int64) bool {
	for n := 0; id > 0 && n < 1024; n++ {
		if id == ancestor {
			return true
		}
		e, err := l.Get(id)
		if err != nil {
			return false
		}
		id = e.Parent
	}
	return false
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
func (l *Library) Refresh(ctx context.Context, id int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkNumberingAvailable(id); err != nil {
		return err
	}
	p, err := l.Path(id)
	if err != nil {
		return err
	}
	e, err := l.Get(id)
	if err != nil || e.Kind != "directory" {
		return fmt.Errorf("不是目录")
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	gen := time.Now().UnixNano()
	fail := func(e error) error {
		_, _ = l.DB.Exec("INSERT INTO directory_state(dir,error) VALUES(?,?) ON CONFLICT(dir) DO UPDATE SET error=excluded.error", id, e.Error())
		return e
	}
	for {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		batch, re := f.ReadDir(128)
		tx, err := l.DB.BeginTx(ctx, nil)
		if err != nil {
			return fail(err)
		}
		for _, de := range batch {
			info, er := de.Info()
			if er != nil {
				tx.Rollback()
				return fail(er)
			}
			snap, er := domain.StatSnapshot(filepath.Join(p, de.Name()))
			if er != nil {
				tx.Rollback()
				return fail(er)
			}
			var eid int64
			var identity string
			er = tx.QueryRow("SELECT id,identity FROM entries WHERE parent=? AND name=? AND present=1", id, de.Name()).Scan(&eid, &identity)
			if er != nil && !errors.Is(er, sql.ErrNoRows) {
				tx.Rollback()
				return fail(er)
			}
			if er == nil && identity != Identity(snap) {
				if _, er = tx.Exec("UPDATE entries SET present=0 WHERE id=?", eid); er != nil {
					tx.Rollback()
					return fail(er)
				}
				eid = 0
			}
			if eid == 0 {
				res, er := tx.Exec("INSERT INTO entries(parent,name,kind,identity,size,mtime,observed) VALUES(?,?,?,?,?,?,?)", id, de.Name(), kindOf(info), Identity(snap), snap.Size, stamp(snap), storage.Now())
				if er != nil {
					tx.Rollback()
					return fail(er)
				}
				eid, _ = res.LastInsertId()
			} else {
				_, er = tx.Exec("UPDATE entries SET version=version+CASE WHEN size<>? OR mtime<>? THEN 1 ELSE 0 END,size=?,mtime=?,kind=?,observed=? WHERE id=?", snap.Size, stamp(snap), snap.Size, stamp(snap), kindOf(info), storage.Now(), eid)
				if er != nil {
					tx.Rollback()
					return fail(er)
				}
			}
			if _, er = tx.Exec("INSERT INTO directory_items VALUES(?,?,?,?,?,?,?)", id, gen, eid, de.Name(), kindOf(info), snap.Size, stamp(snap)); er != nil {
				tx.Rollback()
				return fail(er)
			}
		}
		if err = tx.Commit(); err != nil {
			return fail(err)
		}
		if errors.Is(re, io.EOF) {
			break
		}
		if re != nil {
			return fail(re)
		}
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE entries SET present=0 WHERE parent=? AND id NOT IN(SELECT entry FROM directory_items WHERE dir=? AND generation=?)", id, id, gen); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO directory_state(dir,generation,refreshed,error) VALUES(?,?,?,'') ON CONFLICT(dir) DO UPDATE SET generation=excluded.generation,refreshed=excluded.refreshed,error=''", id, gen, storage.Now()); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM directory_items WHERE dir=? AND generation<>?", id, gen); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Library) StartRefresh(ctx context.Context, id int64) {
	l.refreshMu.Lock()
	if l.refreshing[id] || l.closed || ctx.Err() != nil {
		l.refreshMu.Unlock()
		return
	}
	l.refreshing[id] = true
	l.refreshWG.Add(1)
	l.refreshMu.Unlock()
	go func() {
		defer l.refreshWG.Done()
		defer func() { l.refreshMu.Lock(); delete(l.refreshing, id); l.refreshMu.Unlock() }()
		select {
		case l.scanSlots <- struct{}{}:
			defer func() { <-l.scanSlots }()
		case <-ctx.Done():
			return
		}
		_ = l.Refresh(ctx, id)
	}()
}

type Page struct {
	Total       int     `json:"total"`
	Items       []Entry `json:"items"`
	Next        int     `json:"next"`
	Generation  int64   `json:"generation,string"`
	Refreshing  bool    `json:"refreshing"`
	Refreshed   string  `json:"refreshed"`
	Error       string  `json:"error"`
	Directory   Entry   `json:"directory"`
	Breadcrumbs []Entry `json:"breadcrumbs"`
}

func (l *Library) List(ctx context.Context, id int64, offset, limit int, generation int64, q, kind string, force bool) (Page, error) {
	return l.ListSorted(ctx, id, offset, limit, generation, q, kind, force, false)
}
func (l *Library) ListSorted(ctx context.Context, id int64, offset, limit int, generation int64, q, kind string, force, descending bool) (Page, error) {
	sortBy := "name_asc"
	if descending {
		sortBy = "name_desc"
	}
	return l.ListOrdered(ctx, id, offset, limit, generation, q, kind, force, sortBy)
}
func (l *Library) ListOrdered(ctx context.Context, id int64, offset, limit int, generation int64, q, kind string, force bool, sortBy string) (Page, error) {
	p := Page{Items: []Entry{}, Breadcrumbs: []Entry{}}
	var err error
	p.Directory, err = l.Get(id)
	if err != nil {
		return p, err
	}
	if p.Directory.Kind != "directory" {
		return p, fmt.Errorf("不是目录")
	}
	var refreshed sql.NullString
	err = l.DB.QueryRow("SELECT generation,refreshed,error FROM directory_state WHERE dir=?", id).Scan(&p.Generation, &refreshed, &p.Error)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	p.Refreshed = refreshed.String
	at, _ := time.Parse(time.RFC3339Nano, p.Refreshed)
	if force || time.Since(at) > 30*time.Second {
		l.StartRefresh(ctx, id)
	}
	l.refreshMu.Lock()
	p.Refreshing = l.refreshing[id]
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
	if offset > 0 {
		if err = l.DB.QueryRow("SELECT CASE WHEN kind='directory' THEN 0 ELSE 1 END,name,entry,"+value+" FROM directory_items WHERE dir=? AND generation=? AND entry=?", id, p.Generation, offset).Scan(&anchorKind, &anchorName, &anchorID, &anchorValue); err != nil {
			return p, fmt.Errorf("分页位置已失效，请返回第一页")
		}
	}
	filter := "(?='' OR kind=?)"
	if kind == "video" || kind == "other" || kind == "image" {
		videoSQL := `(lower(name) LIKE '%.mp4' OR lower(name) LIKE '%.m4v' OR lower(name) LIKE '%.mkv' OR lower(name) LIKE '%.mov' OR lower(name) LIKE '%.avi' OR lower(name) LIKE '%.webm' OR lower(name) LIKE '%.flv' OR lower(name) LIKE '%.wmv' OR lower(name) LIKE '%.mpg' OR lower(name) LIKE '%.mpeg' OR lower(name) LIKE '%.ts' OR lower(name) LIKE '%.m2ts' OR lower(name) LIKE '%.mts' OR lower(name) LIKE '%.3gp' OR lower(name) LIKE '%.ogv' OR lower(name) LIKE '%.rmvb' OR lower(name) LIKE '%.asf' OR lower(name) LIKE '%.vob')`
		imageSQL := `(lower(name) LIKE '%.jpg' OR lower(name) LIKE '%.jpeg' OR lower(name) LIKE '%.png' OR lower(name) LIKE '%.gif' OR lower(name) LIKE '%.webp' OR lower(name) LIKE '%.avif' OR lower(name) LIKE '%.bmp')`
		if kind == "image" {
			videoSQL = imageSQL
		}
		if kind == "other" {
			videoSQL = "NOT (" + videoSQL + " OR " + imageSQL + ")"
		}
		filter = "(?<>'' AND ?<>'' AND kind='file' AND " + videoSQL + ")"
		if kind == "video" || kind == "image" {
			filter = "(" + filter + " OR kind='directory')"
		}
	}
	if err = l.DB.QueryRow(`SELECT count(*) FROM directory_items WHERE dir=? AND generation=? AND instr(lower(name),lower(?))>0 AND `+filter, id, p.Generation, q, kind, kind).Scan(&p.Total); err != nil {
		return p, err
	}
	order, comparison := "ASC", ">"
	if strings.HasSuffix(sortBy, "_desc") {
		order, comparison = "DESC", "<"
	}
	rows, err := l.DB.Query(`SELECT entry FROM directory_items WHERE dir=? AND generation=? AND instr(lower(name),lower(?))>0 AND `+filter+` AND (?=0 OR CASE WHEN kind='directory' THEN 0 ELSE 1 END>? OR (CASE WHEN kind='directory' THEN 0 ELSE 1 END=? AND (`+value+`,name COLLATE NATURAL_NAME,entry)`+comparison+`(?,?,?))) ORDER BY CASE WHEN kind='directory' THEN 0 ELSE 1 END,`+value+` `+order+`,name COLLATE NATURAL_NAME `+order+`,entry `+order+` LIMIT ?`, id, p.Generation, q, kind, kind, offset, anchorKind, anchorKind, anchorValue, anchorName, anchorID, limit+1)
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
	if len(ids) > limit {
		p.Next = int(ids[limit-1])
		ids = ids[:limit]
	}
	for _, eid := range ids {
		e, er := l.Get(eid)
		if er == nil {
			p.Items = append(p.Items, e)
		}
	}
	for cur := id; cur > 0; {
		e, er := l.Get(cur)
		if er != nil {
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
		if err := l.DB.QueryRow("SELECT parent,name FROM entries WHERE id=?", id).Scan(&parent, &name); err != nil {
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
	case "name_asc", "name_desc":
	default:
		sortBy = "name_asc"
	}
	return value, sortBy
}
