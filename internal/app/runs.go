package app

import (
	"net/http"

	"video-rename/internal/domain"
	"video-rename/internal/job"
	"video-rename/internal/record"
)

func (a *App) runRoutes(api *router) {
	api.HandleFunc("POST /api/runs", func(w http.ResponseWriter, r *http.Request) {
		var v job.Request
		if !decode(w, r, &v) {
			return
		}
		v.Key = requestKey(r)
		id, err := a.Jobs.Create(r.Context(), v, "manual", 0)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusAccepted, map[string]int64{"id": id})
	})
	api.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := queryFields(q, "category", "task", "offset", "limit", "status", "profile", "from", "to", "duration"); err != nil {
			writeError(w, r, err)
			return
		}
		category := q.Get("category")
		if category == "" {
			category = "all"
		}
		v, err := a.Jobs.ListPage(r.Context(), job.ListPageOptions{Category: category, Task: q.Get("task"), Offset: int(number(q.Get("offset"))), Limit: int(number(q.Get("limit"))), Filters: q})
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
				writeError(w, r, domain.InvalidField("confirm", "confirmation_required"))
				return
			}
			id := number(r.PathValue("id"))
			if id <= 0 {
				writeError(w, r, domain.InvalidField("id", "invalid"))
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

func (a *App) items(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if err := queryFields(r.URL.Query(), "offset", "directory", "status"); err != nil {
		writeError(w, r, err)
		return
	}
	snapshot, err := record.New(a.db).RunItems(r.Context(), record.RunQuery{Run: number(r.PathValue("id")), Filters: r.URL.Query()})
	if err != nil {
		writeError(w, r, err)
		return
	}

	paths, err := a.Library.DisplayPaths(r.Context(), snapshot.EntryIDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	attempts, err := job.Attempts(r.Context(), a.db, snapshot.ItemIDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var live map[int64]job.LiveStage
	if status == "running" {
		live = a.Jobs.Live(snapshot.ItemIDs[:len(snapshot.Items)])
	}
	for _, item := range snapshot.Items {
		id := item["id"].(int64)
		item["current_path"] = paths[item["entry"].(int64)]
		item["attempts"] = attempts[id]
		if stage, ok := live[id]; ok {
			item["stage"] = stage
		}
	}
	if recent, ok := snapshot.Extra["recent"].([]map[string]any); ok {
		for _, item := range recent {
			item["attempts"] = len(attempts[item["id"].(int64)])
		}
	}
	send(w, http.StatusOK, snapshot.Data)
}
