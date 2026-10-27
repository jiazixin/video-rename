package app

import (
	"maps"
	"net/http"
	"slices"
	"strings"

	"video-rename/internal/domain"
	"video-rename/internal/jobs"
)

func (a *App) runRoutes(api *router) {
	api.HandleFunc("POST /api/runs", func(w http.ResponseWriter, r *http.Request) {
		var v jobs.Request
		if !decode(w, r, &v) {
			return
		}
		id, err := a.Jobs.Create(r.Context(), v, "manual", 0)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusAccepted, map[string]int64{"id": id})
	})
	api.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		if category := r.URL.Query().Get("category"); category != "" {
			if category != "all" && category != "manual" && category != "automatic" {
				invalid(w, r, "category", "invalid_choice")
				return
			}
			task := r.URL.Query().Get("task")
			if task != "" && task != "rename" && task != "extract" {
				invalid(w, r, "task", "invalid_choice")
				return
			}
			v, err := a.Jobs.ListPage(r.Context(), category, task, int(number(r.URL.Query().Get("page"))), int(number(r.URL.Query().Get("size"))), r.URL.Query())
			if err != nil {
				writeError(w, r, err)
				return
			}
			send(w, http.StatusOK, v)
			return
		}
		v, err := a.Jobs.List(r.Context(), number(r.URL.Query().Get("before")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, v)
	})
	api.HandleFunc("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Jobs.Get(r.Context(), number(r.PathValue("id")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, v)
	})
	api.HandleFunc("GET /api/runs/{id}/items", a.items)
	api.HandleFunc("POST /api/runs/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		result, err := a.Library.RestoreBatch(r.Context(), number(r.PathValue("id")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, result)
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
				invalid(w, r, "confirm", "confirmation_required")
				return
			}
			id := number(r.PathValue("id"))
			if id <= 0 {
				invalid(w, r, "id", "invalid")
				return
			}
			var err error
			if action == "pause" {
				err = a.Jobs.Pause(r.Context(), id)
			} else {
				err = a.Jobs.Resume(r.Context(), id)
			}
			if err != nil {
				writeError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	api.HandleFunc("POST /api/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Jobs.Cancel(r.Context(), number(r.PathValue("id"))); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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

// runItemOperation joins the operation this run item produced. AutoCommit
// journals each collision attempt as run:R:item:I:N; select the latest attempt
// by insertion order (not the textual suffix), once per item. The bounded key
// range includes the final colon to keep item 1 and 10 apart and lets the
// unique request_key index locate only this item's attempts.
const runItemOperation = ` LEFT JOIN operations o ON o.id=(
 SELECT op.id FROM operations op WHERE op.request_key>='run:'||i.run||':item:'||i.id||':'
 AND op.request_key<'run:'||i.run||':item:'||i.id||';'
 ORDER BY op.id DESC LIMIT 1) AND o.run=i.run AND o.entry=i.entry AND o.source='automatic'`

type skipReason struct {
	Reason domain.Record `json:"reason"`
	Count  int64         `json:"count"`
}

func splitRunPath(original string) (string, string) {
	directory, name, found := strings.CutLast(original, " / ")
	if !found {
		return "", original
	}
	return directory, name
}

func (a *App) items(w http.ResponseWriter, r *http.Request) {
	q, run := r.URL.Query(), number(r.PathValue("id"))
	status := q.Get("status")
	if q.Has("view") || !slices.Contains([]string{"", "running", "done", "skipped", "failed"}, status) {
		invalid(w, r, "status", "invalid_choice")
		return
	}
	tx, err := a.DB.Read().BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, err)
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
		writeError(w, r, err)
		return
	}
	var total int64
	var all runDirectory
	for rows.Next() {
		var d runDirectory
		if err = rows.Scan(&d.Path, &d.Total, &d.Done, &d.Failed, &d.Running, &d.Queued, &d.Skipped, &d.Cancelled); err != nil {
			break
		}
		d.Path = strings.TrimSuffix(d.Path, " /")
		all.Total += d.Total
		all.Done += d.Done
		all.Failed += d.Failed
		all.Running += d.Running
		all.Skipped += d.Skipped
		if !q.Has("directory") || q.Get("directory") == d.Path {
			total += map[string]int64{"": d.Total, "running": d.Running, "done": d.Done, "skipped": d.Skipped, "failed": d.Failed}[status]
		}
		directories = append(directories, d)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeError(w, r, err)
		return
	}
	page := min(max(1, number(q.Get("page"))), max(1, (total+99)/100))
	scope := "i.run=?"
	scopeArgs := []any{run}
	if q.Has("directory") {
		prefix := q.Get("directory")
		if prefix != "" {
			prefix += " /"
		}
		scope += " AND " + runDirectoryPrefix + "=?"
		scopeArgs = append(scopeArgs, prefix)
	}
	where, order := scope, runDirectoryPrefix+",i.id"
	if status != "" {
		where += " AND i.status='" + status + "'"
	}
	if status == "running" {
		// Longest-running first, so a stuck file is at the top.
		order = "i.started_ms,i.id"
	}
	args := append(slices.Clone(scopeArgs), (page-1)*100)
	rows, err = tx.QueryContext(r.Context(), `SELECT i.id,i.entry,i.status,i.message,i.original_path,
 MAX(i.started_ms,i.finished_ms),COALESCE(o.new_name,''),COALESCE(o.status,''),COALESCE(o.undone,0)
 FROM run_items i`+runItemOperation+`
 WHERE `+where+` ORDER BY `+order+` LIMIT 100 OFFSET ?`, args...)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := []map[string]any{}
	itemIDs, entryIDs := []int64{}, []int64{}
	for rows.Next() {
		var id, entry, updated, undone int64
		var itemStatus, original, newName, operationStatus string
		var message domain.Record
		if err = rows.Scan(&id, &entry, &itemStatus, &message, &original, &updated, &newName, &operationStatus, &undone); err != nil {
			break
		}
		directory, name := splitRunPath(original)
		out = append(out, map[string]any{"id": id, "entry": entry, "status": itemStatus, "message": message, "original_path": original, "directory": directory, "original_name": name, "new_name": newName, "operation_status": operationStatus, "undone": undone != 0, "updated_ms": updated})
		itemIDs = append(itemIDs, id)
		entryIDs = append(entryIDs, entry)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeError(w, r, err)
		return
	}
	extra := map[string]any{}
	if status == "running" {
		// The latest finished files show the batch is still moving. The
		// status<>'queued' term lets SQLite use the item_logs partial index.
		recent := []map[string]any{}
		rows, err = tx.QueryContext(r.Context(), `SELECT i.id,i.status,i.message,i.original_path,i.started_ms,i.finished_ms,
 COALESCE(o.new_name,''),COALESCE(o.status,''),COALESCE(o.undone,0)
 FROM run_items i`+runItemOperation+`
 WHERE `+scope+` AND i.status<>'queued' AND i.status<>'running'
 ORDER BY MAX(i.started_ms,i.finished_ms) DESC,i.id DESC LIMIT 20`, scopeArgs...)
		if err != nil {
			writeError(w, r, err)
			return
		}
		recentIDs := []int64{}
		for rows.Next() {
			var id, started, finished, undone int64
			var itemStatus, original, newName, operationStatus string
			var message domain.Record
			if err = rows.Scan(&id, &itemStatus, &message, &original, &started, &finished, &newName, &operationStatus, &undone); err != nil {
				break
			}
			directory, name := splitRunPath(original)
			recent = append(recent, map[string]any{"id": id, "status": itemStatus, "message": message, "directory": directory, "original_name": name, "new_name": newName, "operation_status": operationStatus, "undone": undone != 0, "started_ms": started, "finished_ms": finished})
			recentIDs = append(recentIDs, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			writeError(w, r, err)
			return
		}
		itemIDs = append(itemIDs, recentIDs...)
		extra["recent"] = recent
	}
	if status == "skipped" {
		reasons := []skipReason{}
		rows, err = tx.QueryContext(r.Context(), `SELECT i.message,count(*) FROM run_items i
 WHERE `+scope+` AND i.status='skipped' GROUP BY 1 ORDER BY 2 DESC,1 LIMIT 8`, scopeArgs...)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for rows.Next() {
			var reason skipReason
			if err = rows.Scan(&reason.Reason, &reason.Count); err != nil {
				break
			}
			reasons = append(reasons, reason)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			writeError(w, r, err)
			return
		}
		extra["skip_reasons"] = reasons
	}
	if err = tx.Commit(); err != nil {
		writeError(w, r, err)
		return
	}
	paths, err := a.Library.DisplayPaths(r.Context(), entryIDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	attempts, err := jobs.Attempts(r.Context(), a.DB, itemIDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var live map[int64]jobs.LiveStage
	if status == "running" {
		live = a.Jobs.Live(itemIDs[:len(out)])
	}
	for _, item := range out {
		id := item["id"].(int64)
		item["current_path"] = paths[item["entry"].(int64)]
		item["attempts"] = attempts[id]
		if stage, ok := live[id]; ok {
			item["stage"] = stage
		}
	}
	if recent, ok := extra["recent"].([]map[string]any); ok {
		for _, item := range recent {
			item["attempts"] = len(attempts[item["id"].(int64)])
		}
	}
	result := map[string]any{"items": out, "directories": directories, "total": total, "all_total": all.Total,
		"done_total": all.Done, "running_total": all.Running, "skipped_total": all.Skipped, "failed_total": all.Failed, "page": page}
	maps.Copy(result, extra)
	send(w, http.StatusOK, result)
}
