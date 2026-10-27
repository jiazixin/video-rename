package app

import (
	"net/http"
	"strconv"
	"strings"
)

func (a *App) entryStatus(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(parts) > 300 {
		invalid(w, r, "ids", "too_many")
		return
	}
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id < 1 {
			invalid(w, r, "ids", "invalid")
			return
		}
		ids = append(ids, id)
	}
	items, err := a.Library.GetBatch(r.Context(), ids)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, err = a.Library.WithProgressBatch(r.Context(), items)
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, items)
}
func (a *App) markEntries(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Targets []int64 `json:"targets"`
		Marked  *bool   `json:"marked"`
		Key     string  `json:"key"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Marked == nil || len(input.Targets) == 0 || len(input.Targets) > 100 {
		invalid(w, r, "targets", "selection_required")
		return
	}
	for _, id := range input.Targets {
		if id < 1 {
			invalid(w, r, "ids", "invalid")
			return
		}
	}
	if input.Key != "" {
		a.startMark(w, r, input.Key, input.Targets, *input.Marked)
		return
	}
	send(w, http.StatusOK, a.Library.SetProcessed(r.Context(), input.Targets, *input.Marked))
}
