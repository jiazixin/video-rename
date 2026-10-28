package record

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/pagination"
	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

// Extraction is one extraction history item.
type Extraction struct {
	ID         int64         `json:"id"`
	Run        int64         `json:"run"`
	Entry      int64         `json:"entry"`
	Path       string        `json:"path"`
	Source     string        `json:"source"`
	Status     string        `json:"status"`
	Started    string        `json:"started"`
	Finished   string        `json:"finished"`
	DurationMS int64         `json:"duration_ms"`
	Cached     bool          `json:"cached"`
	Frames     int           `json:"frames"`
	Error      domain.Record `json:"error,omitzero"`
}

// Extractions returns completion groups from one database snapshot.
func (s *Store) Extractions(ctx context.Context, q Query) (any, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (any, error) {
		var run int64
		if q.Get("run") != "" {
			var err error
			run, err = strconv.ParseInt(q.Get("run"), 10, 64)
			if err != nil || run <= 0 {
				return nil, domain.InvalidField("run", "invalid_number")
			}
		}
		for _, key := range []string{"from", "to"} {
			if value := q.Get(key); value != "" {
				stamp, err := time.Parse(time.RFC3339Nano, value)
				if err != nil {
					return nil, domain.InvalidField(key, "invalid_date")
				}
				q.Set(key, timefmt.Format(stamp))
			}
		}
		limit := number(q.Get("limit"))
		if limit != 50 && limit != 100 && limit != 200 {
			limit = 50
		}
		var err error
		where := ` FROM extraction_records WHERE (?=0 OR run=?) AND (?='' OR instr(lower(path||message),lower(?))>0) AND (?='' OR julianday(started)>=julianday(?)) AND (?='' OR julianday(started)<=julianday(?))`
		args := []any{run, run, q.Get("q"), q.Get("q"), q.Get("from"), q.Get("from"), q.Get("to"), q.Get("to")}
		result := map[string]any{}
		for _, status := range []string{"success", "failure", "running"} {
			groupArgs := append(slices.Clone(args), status)
			var total int64
			if err = tx.QueryRowContext(ctx, "SELECT count(*)"+where+" AND status=?", groupArgs...).Scan(&total); err != nil {
				return nil, err
			}
			if status == "running" {
				result[status] = total
				continue
			}
			offset := pagination.Offset(number(q.Get(status+"_offset")), total, limit)
			rows, err := tx.QueryContext(ctx, `SELECT id,run,entry,path,source,status,started,finished,duration_ms,cached,frames,message`+where+` AND status=? ORDER BY id DESC LIMIT ? OFFSET ?`, append(groupArgs, limit, offset)...)
			if err != nil {
				return nil, err
			}
			items, err := storage.CollectRows(rows, func(rows *sql.Rows) (Extraction, error) {
				var item Extraction
				err := rows.Scan(&item.ID, &item.Run, &item.Entry, &item.Path, &item.Source, &item.Status, &item.Started, &item.Finished, &item.DurationMS, &item.Cached, &item.Frames, &item.Error)
				return item, err
			})
			if err != nil {
				return nil, err
			}
			result[status] = map[string]any{"items": items, "total": total, "offset": offset, "limit": limit}
		}

		return result, nil
	})
}
