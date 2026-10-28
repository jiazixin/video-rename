package record

import (
	"context"
	"database/sql"
	"strings"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// HistorySelection selects records to remove.
type HistorySelection struct {
	Category string  `json:"category"`
	All      bool    `json:"all"`
	IDs      []int64 `json:"ids"`
}

// ClearHistory removes only the selected history category.
func (s *Store) ClearHistory(ctx context.Context, input HistorySelection) (int64, error) {
	if input.Category != "success" && input.Category != "failure" {
		return 0, domain.InvalidField("category", "invalid_choice")
	}
	if (input.All && len(input.IDs) > 0) || (!input.All && (len(input.IDs) == 0 || len(input.IDs) > 200)) {
		return 0, domain.InvalidField("ids", "selection_required")
	}
	query := "DELETE FROM events WHERE category=?"
	args := []any{input.Category}
	if !input.All {
		for _, id := range input.IDs {
			if id < 1 {
				return 0, domain.InvalidField("ids", "invalid")
			}
			args = append(args, id)
		}
		query += " AND id IN (" + strings.TrimRight(strings.Repeat("?,", len(input.IDs)), ",") + ")"
	}
	return storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (int64, error) {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	})
}

// Batches includes every batch represented by the selected history.
func (s *Store) Batches(ctx context.Context, extraction bool) ([]int64, error) {
	query := "SELECT DISTINCT run FROM events WHERE run>0 AND category IN ('success','failure') ORDER BY run DESC"
	if extraction {
		query = "SELECT DISTINCT run FROM extraction_records WHERE run>0 ORDER BY run DESC"
	}
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) ([]int64, error) {
		rows, err := tx.QueryContext(ctx, query)
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

// HistoryDiagnostics returns the redacted request, response and processing evidence.
func (s *Store) HistoryDiagnostics(ctx context.Context, id int64) (map[string]string, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (map[string]string, error) {
		var request, response, processing string
		err := tx.QueryRowContext(ctx, "SELECT raw_request,raw_response,processing_log FROM events WHERE id=? AND category IN ('success','failure')", id).Scan(&request, &response, &processing)
		return map[string]string{"request": request, "response": response, "processing": processing}, err
	})
}
