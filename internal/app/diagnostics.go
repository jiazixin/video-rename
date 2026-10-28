package app

import (
	"net/http"

	"video-rename/internal/record"
)

func (a *App) logDiagnostics(w http.ResponseWriter, r *http.Request) {
	value, err := record.New(a.db).HistoryDiagnostics(r.Context(), number(r.PathValue("id")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, value)
}
