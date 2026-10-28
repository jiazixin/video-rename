package app

import (
	"net/http"

	"video-rename/internal/record"
)

func (a *App) clearHistory(w http.ResponseWriter, r *http.Request) {
	var input record.HistorySelection
	if !decode(w, r, &input) {
		return
	}
	n, err := record.New(a.db).ClearHistory(r.Context(), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, map[string]any{"deleted": n})
}
