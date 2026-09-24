package library

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func IsImage(name string) bool {
	return strings.Contains("|jpg|jpeg|png|gif|webp|avif|bmp|", "|"+strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")+"|")
}

// IsAudio mirrors playback.go's audioExts; the lists must stay in sync.
func IsAudio(name string) bool {
	return strings.Contains("|mp3|m4a|m4b|aac|flac|wav|wave|ogg|oga|opus|wma|aiff|aif|caf|", "|"+strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")+"|")
}

// AdjacentImage reads a single neighboring image from the current directory index.
// File names are unique within a live parent; the parent/name index bounds traversal.
func (l *Library) AdjacentImage(id int64, previous bool) (Entry, error) {
	current, err := l.Get(id)
	if err != nil {
		return Entry{}, err
	}
	if current.Kind != "file" || !IsImage(current.Name) {
		return Entry{}, fmt.Errorf("当前文件不是图片")
	}
	order, compare := "ASC", ">"
	if previous {
		order, compare = "DESC", "<"
	}
	base := `SELECT id FROM entries WHERE parent=? AND present=1 AND kind='file' AND (lower(name) LIKE '%.jpg' OR lower(name) LIKE '%.jpeg' OR lower(name) LIKE '%.png' OR lower(name) LIKE '%.gif' OR lower(name) LIKE '%.webp' OR lower(name) LIKE '%.avif' OR lower(name) LIKE '%.bmp')`
	var next int64
	err = l.DB.QueryRow(base+" AND name COLLATE NATURAL_NAME"+compare+"? ORDER BY name COLLATE NATURAL_NAME "+order+" LIMIT 1", current.Parent, current.Name).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		err = l.DB.QueryRow(base+" ORDER BY name COLLATE NATURAL_NAME "+order+" LIMIT 1", current.Parent).Scan(&next)
	}
	if err != nil {
		return Entry{}, err
	}
	return l.Get(next)
}
