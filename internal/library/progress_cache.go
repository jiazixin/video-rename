package library

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"video-rename/internal/domain"
)

const directoryCacheLimit = 8192

type directDirectoryCache struct {
	revision, generation int64
	progress             DirectoryProgress
	contents             DirectoryContents
}

// Read directory topology and revisions in one snapshot. Only changed directories
// revisit file rows; subtree summation visits directories, not every media item.
func (l *Library) cachedDirectoryProcessing(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]*directoryProcessing, map[int64]*DirectoryContents, error) {
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE dirs(id,parent) AS (
 SELECT id,parent FROM entries WHERE id IN (SELECT value FROM json_each(?)) AND present=1 AND kind='directory'
 UNION SELECT e.id,e.parent FROM entries e JOIN dirs d ON e.parent=d.id WHERE e.present=1 AND e.kind='directory'
 ) SELECT d.id,d.parent,COALESCE(v.revision,0),COALESCE(s.generation,0),COALESCE(s.error,'')
 FROM dirs d LEFT JOIN directory_revision v ON v.dir=d.id LEFT JOIN directory_state s ON s.dir=d.id`, string(raw))
	if err != nil {
		return nil, nil, err
	}
	nodes := map[int64]*directoryProcessing{}
	values := map[int64]directDirectoryCache{}
	missing := []int64{}
	l.progressCacheMu.Lock()
	for rows.Next() {
		var id, parent int64
		var v directDirectoryCache
		var scanError string
		if err = rows.Scan(&id, &parent, &v.revision, &v.generation, &scanError); err != nil {
			break
		}
		n := &directoryProcessing{parent: parent}
		if v.generation == 0 || scanError != "" {
			n.progress.Unknown = 1
		}
		nodes[id] = n
		if old, ok := l.progressCache[id]; ok && old.revision == v.revision && old.generation == v.generation {
			v = old
		} else {
			missing = append(missing, id)
		}
		values[id] = v
	}
	l.progressCacheMu.Unlock()
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(missing) > 0 {
		raw, err = json.Marshal(missing)
		if err != nil {
			return nil, nil, err
		}
		rows, err = tx.QueryContext(ctx, `SELECT e.parent,COUNT(*),
 SUM(COALESCE(m.source IN ('marked','manual','automatic'),0)),
 SUM(EXISTS(SELECT 1 FROM run_items i JOIN runs r ON r.id=i.run WHERE i.entry=e.id AND i.status IN ('queued','running') AND r.status IN `+domain.ActiveRunStatusesSQL+`))
 FROM json_each(?) d JOIN entries e ON e.parent=d.value
 LEFT JOIN marks m ON m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime
 WHERE e.present=1 AND e.kind='file' AND video_rename_media_kind(e.name)=1 GROUP BY e.parent`, string(raw))
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id int64
			var p DirectoryProgress
			if err = rows.Scan(&id, &p.Total, &p.Processed, &p.Active); err != nil {
				break
			}
			v := values[id]
			v.progress = p
			values[id] = v
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, nil, err
		}
		rows, err = tx.QueryContext(ctx, `SELECT s.dir,
 COALESCE(SUM(i.kind='directory'),0),
 COALESCE(SUM(i.kind='file' AND video_rename_media_kind(i.name)=1),0),
 COALESCE(SUM(i.kind='file' AND video_rename_media_kind(i.name)=2),0),
 COALESCE(SUM(i.kind='file' AND video_rename_media_kind(i.name)=3),0)
 FROM json_each(?) d JOIN directory_state s ON s.dir=d.value
 LEFT JOIN directory_items i ON i.dir=s.dir AND i.generation=s.generation
 WHERE s.generation>0 GROUP BY s.dir`, string(raw))
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id int64
			var c DirectoryContents
			if err = rows.Scan(&id, &c.Directories, &c.Videos, &c.Images, &c.Audio); err != nil {
				break
			}
			v := values[id]
			v.contents = c
			values[id] = v
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, nil, err
		}
		l.progressCacheMu.Lock()
		if l.progressCache == nil {
			l.progressCache = make(map[int64]directDirectoryCache)
		}
		for _, id := range missing {
			v := values[id]
			old, ok := l.progressCache[id]
			if ok && (old.revision > v.revision || old.generation > v.generation) {
				continue
			}
			if !ok && len(l.progressCache) >= directoryCacheLimit {
				for key := range l.progressCache {
					delete(l.progressCache, key)
					break
				}
			}
			l.progressCache[id] = v
		}
		l.progressCacheMu.Unlock()
	}
	contents := map[int64]*DirectoryContents{}
	for id, n := range nodes {
		v := values[id]
		n.progress.Total, n.progress.Processed, n.progress.Active = v.progress.Total, v.progress.Processed, v.progress.Active
		if n.progress.Unknown == 0 {
			c := v.contents
			contents[id] = &c
		}
	}
	return nodes, contents, sumDirectoryProcessing(ctx, nodes, ids)
}
