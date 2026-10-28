package app

import (
	"net/http"

	"video-rename/internal/record"
)

func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	value, err := record.New(a.db).History(r.Context(), r.URL.Query())
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, value)
}
