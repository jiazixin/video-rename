// Package record reads and maintains the history and run-record projections.
package record

import (
	"context"
	"database/sql"
	"net/url"
	"strconv"

	"video-rename/internal/storage"
)

// Store provides history queries without exposing database connections.
type Store struct{ db *storage.Store }

// New creates a record reader over the application store.
func New(db *storage.Store) *Store { return &Store{db: db} }

// number reads a numeric filter from a Query validated by the HTTP boundary.
// An omitted optional filter stays zero for the query's default normalization.
func number(value string) int64 {
	if value == "" {
		return 0
	}
	// Query's caller rejects malformed integer fields before entering this store.
	valueNumber, _ := strconv.ParseInt(value, 10, 64)
	return valueNumber
}

// Diagnostics returns the extraction's stored processing evidence.
func (s *Store) Diagnostics(ctx context.Context, id int64) (string, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (string, error) {
		var detail string
		err := tx.QueryRowContext(ctx, "SELECT processing_log FROM extraction_records WHERE id=?", id).Scan(&detail)
		return detail, err
	})
}

// ClearExtractions removes completed extraction records.
func (s *Store) ClearExtractions(ctx context.Context) (int64, error) {
	return storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (int64, error) {
		result, err := tx.ExecContext(ctx, "DELETE FROM extraction_records WHERE status IN ('success','failure')")
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	})
}

// Query is a validated HTTP query transported as named filters.
type Query = url.Values
