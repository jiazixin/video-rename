package library

import (
	"context"
	"path/filepath"

	"video-rename/internal/domain"
)

// DirectoryNumbering is the numbering of one directory within a tree.
type DirectoryNumbering struct {
	Numbering
	Path string `json:"path"`
}

// PreviewNumberingTree refreshes every real directory, including ones never
// visited in the UI. Parent-first order keeps parent fingerprints valid while
// applying batches (renaming in a child changes that child's directory mtime).
func (l *Library) PreviewNumberingTree(ctx context.Context, parent int64, settings domain.NumberingSettings) ([]DirectoryNumbering, error) {
	return l.PreviewNumberingTreeWithProgress(ctx, parent, settings, nil)
}

// PreviewNumberingTreeWithProgress previews numbering for every directory under
// parent and reports progress.
func (l *Library) PreviewNumberingTreeWithProgress(ctx context.Context, parent int64, settings domain.NumberingSettings, progress func(int64, int, int)) ([]DirectoryNumbering, error) {
	visited, files := 0, 0
	if err := domain.ValidateNumberingSettings(settings); err != nil {
		return nil, err
	}
	result := []DirectoryNumbering{}
	pending := []int64{parent}
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(id, visited, files)
		}
		if err := l.Refresh(ctx, id); err != nil {
			return nil, err
		}
		path, err := l.Path(ctx, id)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(l.Root, path)
		if err != nil {
			return nil, err
		}
		rows, err := l.DB.QueryContext(ctx, `SELECT e.id,e.kind,e.name,COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),'') FROM entries e WHERE e.parent=? AND e.present=1 ORDER BY e.name COLLATE NATURAL_NAME,e.id`, id)
		if err != nil {
			return nil, err
		}
		hasMedia := false
		for rows.Next() {
			var child int64
			var kind, name, processed string
			if err = rows.Scan(&child, &kind, &name, &processed); err != nil {
				rows.Close()
				return nil, err
			}
			if kind == "directory" {
				pending = append(pending, child)
			} else if kind == "file" && (IsImage(name) || IsAudio(name)) && processed != "marked" {
				hasMedia = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		visited++
		if !hasMedia {
			continue
		}
		b, err := l.PreviewNumbering(ctx, NumberingRequest{Parent: id, Media: "mixed", Sort: "name_asc", Settings: settings})
		if err != nil {
			return nil, inDirectory(err, rel)
		}
		result = append(result, DirectoryNumbering{Numbering: b, Path: rel})
		files += b.Total
		if progress != nil {
			progress(id, visited, files)
		}
	}
	return result, nil
}

// NumberingTreeHistory returns the tree numberings recorded under parent.
func (l *Library) NumberingTreeHistory(ctx context.Context, parent int64) ([]DirectoryNumbering, error) {
	if _, err := l.Path(ctx, parent); err != nil {
		return nil, err
	}
	rows, err := l.DB.QueryContext(ctx, `WITH RECURSIVE dirs(id) AS (SELECT ? UNION ALL SELECT e.id FROM entries e JOIN dirs d ON e.parent=d.id WHERE e.present=1 AND e.kind='directory')
	SELECT b.id FROM image_numberings b WHERE b.parent IN (SELECT id FROM dirs) AND b.status<>'preview' ORDER BY b.created DESC,b.id DESC LIMIT 100`, parent)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []DirectoryNumbering{}
	for _, id := range ids {
		b, err := l.Numbering(ctx, id)
		if err != nil {
			return nil, err
		}
		path, err := l.Path(ctx, b.Parent)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(l.Root, path)
		if err != nil {
			return nil, err
		}
		b.Items = nil
		result = append(result, DirectoryNumbering{Numbering: b, Path: rel})
	}
	return result, nil
}
