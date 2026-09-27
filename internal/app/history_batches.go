package app

import "net/http"

// logBatches includes all batches represented in history, beyond the current page.
func (a *App) logBatches(w http.ResponseWriter, r *http.Request) {
	query := "SELECT DISTINCT run FROM events WHERE run>0 AND category IN ('success','failure') ORDER BY run DESC"
	if r.URL.Query().Get("category") == "extraction" {
		query = "SELECT DISTINCT run FROM extraction_records WHERE run>0 ORDER BY run DESC"
	}
	rows, err := a.DB.Read().QueryContext(r.Context(), query)
	if err != nil {
		failure(w, err)
		return
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			failure(w, err)
			return
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		failure(w, err)
		return
	}
	send(w, 200, out)
}
