package app

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"video-rename/internal/domain"
)

type extractionRecord struct {
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

func (a *App) extractions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var run int64
	if q.Get("run") != "" {
		var err error
		run, err = strconv.ParseInt(q.Get("run"), 10, 64)
		if err != nil || run <= 0 {
			invalid(w, r, "run", "invalid_number")
			return
		}
	}
	for _, key := range []string{"from", "to"} {
		if value := q.Get(key); value != "" {
			stamp, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				invalid(w, r, key, "invalid_date")
				return
			}
			q.Set(key, stamp.UTC().Format(time.RFC3339Nano))
		}
	}
	limit := number(q.Get("limit"))
	if limit != 50 && limit != 100 && limit != 200 {
		limit = 50
	}
	tx, err := a.DB.Read().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer tx.Rollback()
	where := ` FROM extraction_records WHERE (?=0 OR run=?) AND (?='' OR instr(lower(path||message),lower(?))>0) AND (?='' OR julianday(started)>=julianday(?)) AND (?='' OR julianday(started)<=julianday(?))`
	args := []any{run, run, q.Get("q"), q.Get("q"), q.Get("from"), q.Get("from"), q.Get("to"), q.Get("to")}
	result := map[string]any{}
	for _, status := range []string{"success", "failure", "running"} {
		groupArgs := append(slices.Clone(args), status)
		var total int64
		if err = tx.QueryRowContext(r.Context(), "SELECT count(*)"+where+" AND status=?", groupArgs...).Scan(&total); err != nil {
			writeError(w, r, err)
			return
		}
		if status == "running" {
			result[status] = total
			continue
		}
		before := number(q.Get(status + "_before"))
		if before <= 0 {
			before = 1 << 62
		}
		page, offset := int64(1), int64(0)
		if q.Has(status + "_page") {
			page = min(max(1, number(q.Get(status+"_page"))), max(1, (total+limit-1)/limit))
			offset = (page - 1) * limit
			before = 1 << 62
		}
		rows, err := tx.QueryContext(r.Context(), `SELECT id,run,entry,path,source,status,started,finished,duration_ms,cached,frames,message`+where+` AND status=? AND id<? ORDER BY id DESC LIMIT ? OFFSET ?`, append(groupArgs, before, limit+1, offset)...)
		if err != nil {
			writeError(w, r, err)
			return
		}
		items := []extractionRecord{}
		for rows.Next() {
			var item extractionRecord
			if err = rows.Scan(&item.ID, &item.Run, &item.Entry, &item.Path, &item.Source, &item.Status, &item.Started, &item.Finished, &item.DurationMS, &item.Cached, &item.Frames, &item.Error); err != nil {
				break
			}
			items = append(items, item)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			writeError(w, r, err)
			return
		}
		next := int64(0)
		if int64(len(items)) > limit {
			items = items[:limit]
			next = items[len(items)-1].ID
		}
		result[status] = map[string]any{"items": items, "total": total, "next": next, "page": page}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, http.StatusOK, result)
}

func (a *App) extractionDetail(w http.ResponseWriter, r *http.Request) {
	var detail string
	if err := a.DB.Read().QueryRowContext(r.Context(), "SELECT processing_log FROM extraction_records WHERE id=?", number(r.PathValue("id"))).Scan(&detail); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, http.StatusOK, map[string]string{"processing_log": detail})
}

func (a *App) clearExtractions(w http.ResponseWriter, r *http.Request) {
	var input struct {
		All bool `json:"all"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !input.All {
		invalid(w, r, "all", "confirmation_required")
		return
	}
	result, err := a.DB.ExecContext(r.Context(), "DELETE FROM extraction_records WHERE status IN ('success','failure')")
	if err != nil {
		writeError(w, r, err)
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, map[string]int64{"deleted": n})
}
