package app

import (
	"net/http"

	"video-rename/internal/record"
)

func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	if err := queryFields(r.URL.Query(), "category", "offset", "limit", "q", "action", "model_source", "run", "from", "to"); err != nil {
		writeError(w, r, err)
		return
	}
	value, err := record.New(a.db).History(r.Context(), r.URL.Query())
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, value)
}
