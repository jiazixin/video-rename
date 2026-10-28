package library

import (
	"context"
	"database/sql"
	"errors"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// DirectoryState reads the published index state without accessing media storage.
type DirectoryState struct {
	Generation int64         `json:"generation,string"`
	Refreshed  string        `json:"refreshed"`
	Refreshing bool          `json:"refreshing"`
	Error      domain.Record `json:"error,omitzero"`
}

// DirectoryState returns the indexing state of directory id.
func (l *Library) DirectoryState(ctx context.Context, id int64) (DirectoryState, error) {
	var state DirectoryState
	var kind string
	if err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT kind FROM entries WHERE id=? AND present=1", id).Scan(&kind)
	}); err != nil {
		return state, err
	}
	if kind != "directory" {
		return state, errNotDirectory
	}
	var refreshed sql.NullString
	err := l.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT generation,refreshed,error FROM directory_state WHERE dir=?", id).Scan(&state.Generation, &refreshed, &state.Error)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	state.Refreshed = refreshed.String
	l.refreshMu.Lock()
	state.Refreshing = l.refreshing[id] != nil || l.scanning[id]
	l.refreshMu.Unlock()
	return state, nil
}

// ChildDirectories returns the next published directory page after entry after.
func (l *Library) ChildDirectories(ctx context.Context, parent, after int64) ([]int64, error) {
	return storage.ReadValue(ctx, l.db, func(tx *sql.Tx) ([]int64, error) {
		rows, err := tx.QueryContext(ctx, `SELECT i.entry FROM directory_items i
 JOIN directory_state s ON s.dir=i.dir AND s.generation=i.generation
 JOIN entries e ON e.id=i.entry AND e.present=1 AND e.kind='directory'
 WHERE i.dir=? AND i.kind='directory' AND i.entry>? ORDER BY i.entry LIMIT 128`, parent, after)
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, func(rows *sql.Rows) (int64, error) {
			var id int64
			err := rows.Scan(&id)
			return id, err
		})
	})
}
