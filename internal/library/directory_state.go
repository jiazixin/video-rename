package library

import (
	"context"
	"database/sql"
	"errors"

	"video-rename/internal/domain"
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
	if err := l.DB.Read().QueryRowContext(ctx, "SELECT kind FROM entries WHERE id=? AND present=1", id).Scan(&kind); err != nil {
		return state, err
	}
	if kind != "directory" {
		return state, errNotDirectory
	}
	var refreshed sql.NullString
	err := l.DB.Read().QueryRowContext(ctx, "SELECT generation,refreshed,error FROM directory_state WHERE dir=?", id).Scan(&state.Generation, &refreshed, &state.Error)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state, err
	}
	state.Refreshed = refreshed.String
	l.refreshMu.Lock()
	state.Refreshing = l.refreshing[id] || l.scanning[id]
	l.refreshMu.Unlock()
	return state, nil
}
