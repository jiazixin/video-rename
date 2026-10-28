package app

import (
	"net/http"

	"video-rename/internal/record"
)

func (a *App) logBatches(w http.ResponseWriter, r *http.Request) {
	value, err := record.New(a.db).Batches(r.Context(), r.URL.Query().Get("category") == "extraction")
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, value)
}
