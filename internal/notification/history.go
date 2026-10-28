package notification

import (
	"context"
	"database/sql"

	"video-rename/internal/storage"
)

// HistoryPage is a page of notification deliveries.
type HistoryPage struct {
	Items []Delivery `json:"items"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}

// ListHistoryPage counts and reads deliveries in the same database snapshot.
func (s *Service) ListHistoryPage(ctx context.Context, page, size int) (HistoryPage, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (HistoryPage, error) {
		out := HistoryPage{Items: []Delivery{}, Page: max(1, page), Size: size}
		if size != 50 && size != 100 && size != 200 {
			out.Size = 50
		}
		var err error
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_deliveries").Scan(&out.Total); err != nil {
			return out, err
		}
		out.Page = min(out.Page, max(1, (out.Total+out.Size-1)/out.Size))
		rows, err := tx.QueryContext(ctx, "SELECT id,kind,run,status,attempts,error,updated FROM notification_deliveries ORDER BY id DESC LIMIT ? OFFSET ?", out.Size, (out.Page-1)*out.Size)
		if err != nil {
			return out, err
		}
		out.Items, err = storage.CollectRows(rows, scanDelivery)
		return out, err
	})
}
