package app

import (
	"net/http"
	"strconv"
	"strings"
)

func (a *App) entryStatus(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(parts) > 300 {
		send(w, 400, map[string]string{"message": "最多查询 300 个条目"})
		return
	}
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id < 1 {
			send(w, 400, map[string]string{"message": "条目编号无效"})
			return
		}
		ids = append(ids, id)
	}
	items, err := a.Library.GetBatch(r.Context(), ids)
	if err != nil {
		failure(w, err)
		return
	}
	items, err = a.Library.WithProgressBatch(r.Context(), items)
	if err != nil {
		failure(w, err)
		return
	}
	send(w, 200, items)
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
		send(w, 400, map[string]string{"message": "请选择 1–100 个条目并指定标记状态"})
		return
	}
	for _, id := range input.Targets {
		if id < 1 {
			send(w, 400, map[string]string{"message": "条目编号无效"})
			return
		}
	}
	if input.Key != "" {
		a.startMark(w, input.Key, input.Targets, *input.Marked)
		return
	}
	send(w, 200, a.Library.SetProcessed(r.Context(), input.Targets, *input.Marked))
}
