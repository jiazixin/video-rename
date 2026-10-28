package library

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"video-rename/internal/domain"
)

// IsImage reports whether name has an image extension.
func IsImage(name string) bool {
	return strings.Contains("|jpg|jpeg|png|gif|webp|avif|bmp|", "|"+strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")+"|")
}

// IsAudio mirrors playback.go's audioExts; the lists must stay in sync.
func IsAudio(name string) bool {
	return strings.Contains("|mp3|m4a|m4b|aac|flac|wav|wave|ogg|oga|opus|wma|aiff|aif|caf|", "|"+strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")+"|")
}

// AdjacentImage reads a single neighboring image from the current directory index.
// File names are unique within a live parent; the parent/name index bounds traversal.
func (l *Library) AdjacentImage(ctx context.Context, id int64, previous bool) (Entry, error) {
	current, err := l.Get(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if current.Kind != "file" || !IsImage(current.Name) {
		return Entry{}, domain.Fail(domain.KindInvalid, domain.Record{Code: "not_image", Message: "entry is not an image"})
	}
	order, compare := "ASC", ">"
	if previous {
		order, compare = "DESC", "<"
	}
	base := `SELECT id FROM entries WHERE parent=? AND present=1 AND kind='file' AND (lower(name) LIKE '%.jpg' OR lower(name) LIKE '%.jpeg' OR lower(name) LIKE '%.png' OR lower(name) LIKE '%.gif' OR lower(name) LIKE '%.webp' OR lower(name) LIKE '%.avif' OR lower(name) LIKE '%.bmp')`
	var next int64
	err = l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, base+" AND name COLLATE NATURAL_NAME"+compare+"? ORDER BY name COLLATE NATURAL_NAME "+order+" LIMIT 1", current.Parent, current.Name).Scan(&next)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = l.db.View(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, base+" ORDER BY name COLLATE NATURAL_NAME "+order+" LIMIT 1", current.Parent).Scan(&next)
		})
	}
	if err != nil {
		return Entry{}, err
	}
	return l.Get(ctx, next)
}
