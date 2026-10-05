package app

import (
	"net/http"
	"video-rename/internal/jobs"
)

func (a *App) runRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/runs", func(w http.ResponseWriter, r *http.Request) {
		var v jobs.Request
		if !decode(w, r, &v) {
			return
		}
		id, err := a.Jobs.Create(v, "manual", 0)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 202, map[string]int64{"id": id})
	})
	api.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		if category := r.URL.Query().Get("category"); category != "" {
			if category != "all" && category != "manual" && category != "automatic" {
				send(w, 400, map[string]string{"message": "运行分类无效"})
				return
			}
			task := r.URL.Query().Get("task")
			if task != "" && task != "rename" && task != "extract" {
				send(w, 400, map[string]string{"message": "任务类型无效"})
				return
			}
			v, err := a.Jobs.ListPage(r.Context(), category, task, int(number(r.URL.Query().Get("page"))), int(number(r.URL.Query().Get("size"))), r.URL.Query())
			if err != nil {
				failure(w, err)
				return
			}
			send(w, 200, v)
			return
		}
		v, err := a.Jobs.List(r.Context(), number(r.URL.Query().Get("before")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	api.HandleFunc("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Jobs.Get(r.Context(), number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	api.HandleFunc("GET /api/runs/{id}/items", a.items)
	api.HandleFunc("POST /api/runs/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		result, err := a.Library.RestoreBatch(r.Context(), number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, result)
	})
	for _, action := range []string{"pause", "resume"} {
		api.HandleFunc("POST /api/runs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Confirm bool `json:"confirm"`
			}
			if !decode(w, r, &request) {
				return
			}
			if !request.Confirm {
				send(w, 400, map[string]string{"message": "请确认批次操作"})
				return
			}
			id := number(r.PathValue("id"))
			if id <= 0 {
				send(w, 400, map[string]string{"message": "批次编号无效"})
				return
			}
			var err error
			if action == "pause" {
				err = a.Jobs.Pause(id)
			} else {
				err = a.Jobs.Resume(id)
			}
			if err != nil {
				failure(w, err)
				return
			}
			send(w, 200, map[string]bool{"ok": true})
		})
	}
	api.HandleFunc("POST /api/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Jobs.Cancel(number(r.PathValue("id"))); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
}

func (a *App) items(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, run := number(q.Get("after")), number(r.PathValue("id"))
	view := q.Get("view")
	where, order := "run=?", "id"
	switch view {
	case "":
	case "logs":
		where += " AND status<>'queued'"
		order = "MAX(started_ms,finished_ms) DESC,id DESC"
	case "failures":
		where += " AND status='failed'"
		order = "MAX(started_ms,finished_ms) DESC,id DESC"
	default:
		send(w, 400, map[string]string{"message": "批次记录类型无效"})
		return
	}
	paged := q.Has("page") || view != ""
	tx, err := a.DB.Read().BeginTx(r.Context(), nil)
	if err != nil {
		failure(w, err)
		return
	}
	defer tx.Rollback()
	var total, logTotal int64
	if view == "" {
		err = tx.QueryRowContext(r.Context(), "SELECT count(*),count(*) FILTER (WHERE status<>'queued') FROM run_items WHERE run=?", run).Scan(&total, &logTotal)
	} else {
		err = tx.QueryRowContext(r.Context(), "SELECT count(*) FROM run_items WHERE "+where, run).Scan(&total)
	}
	if err != nil {
		failure(w, err)
		return
	}
	page, offset := int64(1), int64(0)
	if paged {
		page = min(max(1, number(q.Get("page"))), max(1, (total+99)/100))
		offset = (page - 1) * 100
		after = 0
	}
	pageWhere := where
	args := []any{run}
	if after > 0 {
		pageWhere += " AND id>?"
		args = append(args, after)
	}
	args = append(args, offset)
	rows, err := tx.QueryContext(r.Context(), "SELECT id,entry,status,message,original_path,MAX(started_ms,finished_ms) FROM run_items WHERE "+pageWhere+" ORDER BY "+order+" LIMIT 100 OFFSET ?", args...)
	if err != nil {
		failure(w, err)
		return
	}
	out := []map[string]any{}
	for rows.Next() {
		var id, entry, updated int64
		var status, message, original string
		if err = rows.Scan(&id, &entry, &status, &message, &original, &updated); err != nil {
			break
		}
		out = append(out, map[string]any{"id": id, "entry": entry, "status": status, "message": message, "original_path": original, "updated_ms": updated})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		failure(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		failure(w, err)
		return
	}
	itemIDs, entryIDs := make([]int64, 0, len(out)), make([]int64, 0, len(out))
	for _, item := range out {
		itemIDs = append(itemIDs, item["id"].(int64))
		entryIDs = append(entryIDs, item["entry"].(int64))
	}
	paths, err := a.Library.DisplayPaths(r.Context(), entryIDs)
	if err != nil {
		failure(w, err)
		return
	}
	attempts, err := jobs.Attempts(r.Context(), a.DB, itemIDs)
	if err != nil {
		failure(w, err)
		return
	}
	for _, item := range out {
		item["current_path"] = paths[item["entry"].(int64)]
		item["attempts"] = attempts[item["id"].(int64)]
	}
	if paged {
		result := map[string]any{"items": out, "total": total, "page": page}
		if view == "" {
			result["log_total"] = logTotal
		}
		send(w, 200, result)
		return
	}
	send(w, 200, out)
}
