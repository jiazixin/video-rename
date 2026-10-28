package notification

import (
	"context"
	"database/sql"

	"video-rename/internal/pagination"
	"video-rename/internal/storage"
)

// HistoryPage is a page of notification deliveries.
type HistoryPage struct {
	Items  []Delivery `json:"items"`
	Total  int        `json:"total"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
}

// ListHistoryPage counts and reads deliveries in the same database snapshot.
func (s *Service) ListHistoryPage(ctx context.Context, page, size int) (HistoryPage, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (HistoryPage, error) {
		out := HistoryPage{Items: []Delivery{}, Offset: max(0, page), Limit: size}
		if size != 50 && size != 100 && size != 200 {
			out.Limit = 50
		}
		var err error
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_deliveries").Scan(&out.Total); err != nil {
			return out, err
		}
		out.Offset = pagination.Offset(out.Offset, out.Total, out.Limit)
		rows, err := tx.QueryContext(ctx, "SELECT id,kind,run,status,attempts,error,updated FROM notification_deliveries ORDER BY id DESC LIMIT ? OFFSET ?", out.Limit, out.Offset)
		if err != nil {
			return out, err
		}
		out.Items, err = storage.CollectRows(rows, scanDelivery)
		return out, err
	})
}
