package app

import (
	"net/http"

	"video-rename/internal/domain"
	"video-rename/internal/record"
)

func (a *App) extractions(w http.ResponseWriter, r *http.Request) {
	value, err := record.New(a.db).Extractions(r.Context(), r.URL.Query())
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, value)
}
func (a *App) extractionDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := record.New(a.db).Diagnostics(r.Context(), number(r.PathValue("id")))
	if err != nil {
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
		writeError(w, r, domain.InvalidField("all", "confirmation_required"))
		return
	}
	n, err := record.New(a.db).ClearExtractions(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, map[string]int64{"deleted": n})
}
