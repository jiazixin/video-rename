package notifications

import "context"

type HistoryPage struct {
	Items []Delivery `json:"items"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}

// ListHistoryPage counts and reads deliveries in the same database snapshot.
func (s *Service) ListHistoryPage(ctx context.Context, page, size int) (HistoryPage, error) {
	out := HistoryPage{Items: []Delivery{}, Page: max(1, page), Size: size}
	if size != 50 && size != 100 && size != 200 {
		out.Size = 50
	}
	tx, err := s.DB.Read().BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_deliveries").Scan(&out.Total); err != nil {
		return out, err
	}
	out.Page = min(out.Page, max(1, (out.Total+out.Size-1)/out.Size))
	rows, err := tx.QueryContext(ctx, "SELECT id,kind,run,status,attempts,error,updated FROM notification_deliveries ORDER BY id DESC LIMIT ? OFFSET ?", out.Size, (out.Page-1)*out.Size)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.Kind, &d.Run, &d.Status, &d.Attempts, &d.Error, &d.Updated); err != nil {
			return out, err
		}
		out.Items = append(out.Items, d)
	}
	return out, rows.Err()
}
