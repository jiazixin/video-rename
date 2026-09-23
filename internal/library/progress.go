package library

import (
	"database/sql"
	"errors"

	"video-rename/internal/discovery"
)

// DirectoryContents counts only immediate children from the last complete index.
type DirectoryContents struct {
	Directories int `json:"directories"`
	Videos      int `json:"videos"`
	Images      int `json:"images"`
}

func (l *Library) directoryContents(id int64) (*DirectoryContents, error) {
	var generation int64
	err := l.DB.QueryRow("SELECT generation FROM directory_state WHERE dir=? AND generation>0 AND error=''", id).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := l.DB.Query("SELECT kind,name FROM directory_items WHERE dir=? AND generation=?", id, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contents := &DirectoryContents{}
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			return nil, err
		}
		switch {
		case kind == "directory":
			contents.Directories++
		case kind == "file" && discovery.IsVideoExtension(name):
			contents.Videos++
		case kind == "file" && IsImage(name):
			contents.Images++
		}
	}
	return contents, rows.Err()
}

// DirectoryProgress summarizes the indexed subtree. Unknown directories prevent
// a partially discovered tree from being presented as fully processed.
type DirectoryProgress struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
	Unknown   int `json:"unknown"`
	Active    int `json:"active"`
}

func (l *Library) WithProgress(e Entry) (Entry, error) {
	if e.Kind != "directory" {
		return e, nil
	}
	rows, err := l.DB.Query(`WITH RECURSIVE tree(id) AS (
 SELECT ? UNION SELECT e.id FROM entries e JOIN tree t ON e.parent=t.id WHERE e.present=1
 ) SELECT e.kind,e.name,
 EXISTS(SELECT 1 FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),
 EXISTS(SELECT 1 FROM directory_state d WHERE d.dir=e.id AND d.generation>0 AND d.error=''),
 EXISTS(SELECT 1 FROM run_items i JOIN runs r ON r.id=i.run WHERE i.entry=e.id AND i.status IN ('queued','running') AND r.status IN ('queued','scanning','running','cancelling'))
 FROM tree JOIN entries e ON e.id=tree.id`, e.ID)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	progress := &DirectoryProgress{}
	for rows.Next() {
		var kind, name string
		var marked, indexed, active bool
		if err = rows.Scan(&kind, &name, &marked, &indexed, &active); err != nil {
			return e, err
		}
		if kind == "directory" && !indexed {
			progress.Unknown++
		}
		if kind == "file" && discovery.IsVideoExtension(name) {
			progress.Total++
			if marked {
				progress.Processed++
			}
			if active {
				progress.Active++
			}
		}
	}
	if err = rows.Err(); err != nil {
		return e, err
	}
	e.Progress = progress
	e.Contents, err = l.directoryContents(e.ID)
	if err != nil {
		return e, err
	}
	return e, nil
}
