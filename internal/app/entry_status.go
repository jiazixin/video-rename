package app

import (
	"net/http"
	"strconv"
	"strings"

	"video-rename/internal/domain"
)

func (a *App) entryStatus(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(parts) > 300 {
		writeError(w, r, domain.InvalidField("ids", "too_many"))
		return
	}
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id < 1 {
			writeError(w, r, domain.InvalidField("ids", "invalid"))
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
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Marked == nil || len(input.Targets) == 0 || len(input.Targets) > 100 {
		writeError(w, r, domain.InvalidField("targets", "selection_required"))
		return
	}
	for _, id := range input.Targets {
		if id < 1 {
			writeError(w, r, domain.InvalidField("ids", "invalid"))
			return
		}
	}
	a.startMark(w, r, markRequestInput{Key: requestKey(r), Targets: input.Targets, Marked: *input.Marked})
}
