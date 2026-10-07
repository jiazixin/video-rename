package app

import (
	"net/http"
	"strings"
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

// The trim character set contains every character except slash, so this leaves
// the display directory prefix (including its separator), without reading the
// current entries tree. queueFiles stores paths joined with " / ".
const runDirectoryPrefix = "rtrim(original_path,replace(original_path,'/',''))"

type runDirectory struct {
	Path      string `json:"path"`
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
	Failed    int64  `json:"failed"`
	Running   int64  `json:"running"`
	Queued    int64  `json:"queued"`
	Skipped   int64  `json:"skipped"`
	Cancelled int64  `json:"cancelled"`
}

func (a *App) items(w http.ResponseWriter, r *http.Request) {
	q, run := r.URL.Query(), number(r.PathValue("id"))
	if q.Has("view") || (q.Get("status") != "" && q.Get("status") != "failed") {
		send(w, 400, map[string]string{"message": "批次记录筛选无效"})
		return
	}
	tx, err := a.DB.Read().BeginTx(r.Context(), nil)
	if err != nil {
		failure(w, err)
		return
	}
	defer tx.Rollback()
	directories := []runDirectory{}
	rows, err := tx.QueryContext(r.Context(), `SELECT `+runDirectoryPrefix+`,count(*),
 count(*) FILTER (WHERE status='done'),count(*) FILTER (WHERE status='failed'),
 count(*) FILTER (WHERE status='running'),count(*) FILTER (WHERE status='queued'),
 count(*) FILTER (WHERE status='skipped'),count(*) FILTER (WHERE status='cancelled')
 FROM run_items WHERE run=? GROUP BY 1 ORDER BY 1`, run)
	if err != nil {
		failure(w, err)
		return
	}
	var total, allTotal, failedTotal int64
	for rows.Next() {
		var d runDirectory
		if err = rows.Scan(&d.Path, &d.Total, &d.Done, &d.Failed, &d.Running, &d.Queued, &d.Skipped, &d.Cancelled); err != nil {
			break
		}
		d.Path = strings.TrimSuffix(d.Path, " /")
		allTotal += d.Total
		failedTotal += d.Failed
		if !q.Has("directory") || q.Get("directory") == d.Path {
			if q.Get("status") == "failed" {
				total += d.Failed
			} else {
				total += d.Total
			}
		}
		directories = append(directories, d)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		failure(w, err)
		return
	}
	page := min(max(1, number(q.Get("page"))), max(1, (total+99)/100))
	where := "i.run=?"
	args := []any{run}
	if q.Get("status") == "failed" {
		where += " AND i.status='failed'"
	}
	if q.Has("directory") {
		prefix := q.Get("directory")
		if prefix != "" {
			prefix += " /"
		}
		where += " AND " + runDirectoryPrefix + "=?"
		args = append(args, prefix)
	}
	args = append(args, (page-1)*100)
	// AutoCommit journals each collision attempt as run:R:item:I:N. Select the
	// latest attempt by insertion order (not the textual suffix), once per item.
	// The bounded key range includes the final colon to keep item 1 and 10 apart
	// and allows the unique request_key index to locate only this item's attempts.
	rows, err = tx.QueryContext(r.Context(), `SELECT i.id,i.entry,i.status,i.message,i.original_path,
 MAX(i.started_ms,i.finished_ms),COALESCE(o.new_name,''),COALESCE(o.status,''),COALESCE(o.undone,0)
 FROM run_items i LEFT JOIN operations o ON o.id=(
 SELECT op.id FROM operations op WHERE op.request_key>='run:'||i.run||':item:'||i.id||':'
 AND op.request_key<'run:'||i.run||':item:'||i.id||';'
 ORDER BY op.id DESC LIMIT 1) AND o.run=i.run AND o.entry=i.entry AND o.source='automatic'
 WHERE `+where+` ORDER BY `+runDirectoryPrefix+`,i.id LIMIT 100 OFFSET ?`, args...)
	if err != nil {
		failure(w, err)
		return
	}
	out := []map[string]any{}
	itemIDs, entryIDs := []int64{}, []int64{}
	for rows.Next() {
		var id, entry, updated, undone int64
		var status, message, original, newName, operationStatus string
		if err = rows.Scan(&id, &entry, &status, &message, &original, &updated, &newName, &operationStatus, &undone); err != nil {
			break
		}
		directory, name, found := strings.CutLast(original, " / ")
		if !found {
			directory, name = "", original
		}
		out = append(out, map[string]any{"id": id, "entry": entry, "status": status, "message": message, "original_path": original, "directory": directory, "original_name": name, "new_name": newName, "operation_status": operationStatus, "undone": undone != 0, "updated_ms": updated})
		itemIDs = append(itemIDs, id)
		entryIDs = append(entryIDs, entry)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		failure(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failure(w, err)
		return
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
	send(w, 200, map[string]any{"items": out, "directories": directories, "total": total, "all_total": allTotal, "failed_total": failedTotal, "page": page})
}
