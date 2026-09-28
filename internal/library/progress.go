package library

import (
	"context"
	"database/sql/driver"
	"encoding/json/v2"
	"fmt"
	"slices"

	"modernc.org/sqlite"
	"video-rename/internal/discovery"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("video_rename_media_kind", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		name, _ := args[0].(string)
		switch {
		case discovery.IsVideoExtension(name):
			return int64(1), nil
		case IsImage(name):
			return int64(2), nil
		case IsAudio(name):
			return int64(3), nil
		default:
			return int64(0), nil
		}
	})
}

// DirectoryContents counts only immediate children from the last complete index.
type DirectoryContents struct {
	Directories int `json:"directories"`
	Videos      int `json:"videos"`
	Images      int `json:"images"`
	Audio       int `json:"audio"`
}

// DirectoryProgress summarizes the indexed subtree. Unknown directories prevent
// a partially discovered tree from being presented as fully processed.
type DirectoryProgress struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
	Unknown   int `json:"unknown"`
	Active    int `json:"active"`
}

// WithProgressBatch visits the union of requested subtrees once. Only per-directory
// totals cross the SQL boundary; overlapping parents share the same aggregates.
// The read transaction keeps counts and published generations in one snapshot.
func (l *Library) WithProgressBatch(ctx context.Context, entries []Entry) ([]Entry, error) {
	entries = slices.Clone(entries)
	ids := []int64{}
	seen := map[int64]bool{}
	for _, e := range entries {
		if e.Kind == "directory" && !seen[e.ID] {
			ids = append(ids, e.ID)
			seen[e.ID] = true
		}
	}
	if len(ids) == 0 {
		return entries, ctx.Err()
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	tx, err := l.DB.Read().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE dirs(id,parent) AS (
 SELECT e.id,e.parent FROM entries e JOIN json_each(?) selected ON selected.value=e.id WHERE e.present=1 AND e.kind='directory'
 UNION SELECT e.id,e.parent FROM entries e JOIN dirs d ON e.parent=d.id WHERE e.present=1 AND e.kind='directory'
 ), counts AS (
 SELECT e.parent,COUNT(*) AS total,
 SUM(EXISTS(SELECT 1 FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime)) AS processed,
 SUM(EXISTS(SELECT 1 FROM run_items i JOIN runs r ON r.id=i.run WHERE i.entry=e.id AND i.status IN ('queued','running') AND r.status IN ('queued','scanning','running','paused','cancelling'))) AS active
 FROM dirs d JOIN entries e ON e.parent=d.id WHERE e.present=1 AND e.kind='file' AND video_rename_media_kind(e.name)=1 GROUP BY e.parent
 ) SELECT d.id,d.parent,COALESCE(c.total,0),COALESCE(c.processed,0),COALESCE(c.active,0),
 NOT EXISTS(SELECT 1 FROM directory_state s WHERE s.dir=d.id AND s.generation>0 AND s.error='')
 FROM dirs d LEFT JOIN counts c ON c.parent=d.id`, string(raw))
	if err != nil {
		return nil, err
	}
	type totals struct {
		parent   int64
		progress DirectoryProgress
		children []int64
		state    int
	}
	nodes := map[int64]*totals{}
	for rows.Next() {
		var id int64
		n := &totals{}
		if err = rows.Scan(&id, &n.parent, &n.progress.Total, &n.progress.Processed, &n.progress.Active, &n.progress.Unknown); err != nil {
			break
		}
		nodes[id] = n
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for id, n := range nodes {
		if parent := nodes[n.parent]; parent != nil {
			parent.children = append(parent.children, id)
		}
	}
	var sum func(int64, int) error
	sum = func(id int64, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := nodes[id]
		if n == nil || n.state == 2 {
			return nil
		}
		if n.state == 1 || depth > 1024 {
			return fmt.Errorf("目录关系无效")
		}
		n.state = 1
		for _, child := range n.children {
			if err := sum(child, depth+1); err != nil {
				return err
			}
			p := nodes[child].progress
			n.progress.Total += p.Total
			n.progress.Processed += p.Processed
			n.progress.Active += p.Active
			n.progress.Unknown += p.Unknown
		}
		n.state = 2
		return nil
	}
	for _, id := range ids {
		if err := sum(id, 0); err != nil {
			return nil, err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT s.dir,
 COALESCE(SUM(i.kind='directory'),0),
 COALESCE(SUM(i.kind='file' AND video_rename_media_kind(i.name)=1),0),
 COALESCE(SUM(i.kind='file' AND video_rename_media_kind(i.name)=2),0),
 COALESCE(SUM(i.kind='file' AND video_rename_media_kind(i.name)=3),0)
 FROM directory_state s JOIN json_each(?) selected ON selected.value=s.dir
 LEFT JOIN directory_items i ON i.dir=s.dir AND i.generation=s.generation
 WHERE s.generation>0 AND s.error='' GROUP BY s.dir`, string(raw))
	if err != nil {
		return nil, err
	}
	contents := map[int64]*DirectoryContents{}
	for rows.Next() {
		var id int64
		c := &DirectoryContents{}
		if err = rows.Scan(&id, &c.Directories, &c.Videos, &c.Images, &c.Audio); err != nil {
			break
		}
		contents[id] = c
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for i, e := range entries {
		if e.Kind != "directory" {
			continue
		}
		p := DirectoryProgress{}
		if n := nodes[e.ID]; n != nil {
			p = n.progress
		}
		entries[i].Progress = &p
		entries[i].Contents = contents[e.ID]
	}
	return entries, nil
}
