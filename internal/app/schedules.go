package app

import (
	"net/http"
	"time"
	"video-rename/internal/jobs"
	"video-rename/internal/scheduler"
)

func (a *App) scheduleRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Scheduler.List()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var p scheduler.Plan
		if !decode(w, r, &p) {
			return
		}
		id, ok := creationOrUpdateID(w, r)
		if !ok {
			return
		}
		p.ID = id
		v, err := a.Scheduler.Save(p)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	}
	api.HandleFunc("POST /api/schedules", save)
	api.HandleFunc("PATCH /api/schedules/{id}", save)
	api.HandleFunc("DELETE /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Scheduler.Delete(number(r.PathValue("id"))); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("POST /api/schedules/preview", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Expression string `json:"expression"`
			Timezone   string `json:"timezone"`
		}
		if !decode(w, r, &v) {
			return
		}
		out, err := scheduler.Preview(v.Expression, v.Timezone, time.Now())
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, out)
	})
	api.HandleFunc("POST /api/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Key string `json:"key"`
		}
		if !decode(w, r, &v) {
			return
		}
		p, err := a.Scheduler.Get(number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		id, err := a.Jobs.Create(jobs.Request{Task: p.Task, ModelConfigID: p.ModelConfigID, Targets: p.Targets, Recursive: p.Recursive, Key: v.Key}, "manual", p.ID)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 202, map[string]int64{"id": id})
	})
}
