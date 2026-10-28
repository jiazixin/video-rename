package app

import (
	"net/http"
	"strconv"
	"time"

	"video-rename/internal/job"
	"video-rename/internal/scheduler"
)

func (a *App) scheduleRoutes(api *router) {
	api.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Scheduler.List(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, v)
	})
	api.HandleFunc("GET /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		value, err := a.Scheduler.Get(r.Context(), number(r.PathValue("id")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, value)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		id, ok := creationOrUpdateID(w, r)
		if !ok {
			return
		}
		var input scheduleInput
		var version int64
		if id != 0 {
			current, err := a.Scheduler.Get(r.Context(), id)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if !matchVersion(w, r, current.Version) {
				return
			}
			input, version = scheduleInputOf(current), current.Version
			if !decodePatch(w, r, &input) {
				return
			}
		} else if !decode(w, r, &input) {
			return
		}
		value, err := a.Scheduler.Save(r.Context(), input.plan(id, version))
		if err != nil {
			writeError(w, r, err)
			return
		}
		if id == 0 {
			w.Header().Set("Location", "/api/schedules/"+strconv.FormatInt(value.ID, 10))
		}
		send(w, creationStatus(r), value)
	}
	api.HandleFunc("POST /api/schedules", save)
	api.HandleFunc("PATCH /api/schedules/{id}", save)
	api.HandleFunc("DELETE /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := number(r.PathValue("id"))
		current, err := a.Scheduler.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !matchVersion(w, r, current.Version) || !decodeEmpty(w, r) {
			return
		}
		if err := a.Scheduler.Delete(r.Context(), id, current.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, out)
	})
	api.HandleFunc("POST /api/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		if !decodeEmpty(w, r) {
			return
		}
		p, err := a.Scheduler.Get(r.Context(), number(r.PathValue("id")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		id, err := a.Jobs.Create(r.Context(), job.Request{Task: p.Task, ModelConfigID: p.ModelConfigID, Targets: p.Targets, Recursive: p.Recursive, Key: requestKey(r)}, "manual", p.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusAccepted, map[string]int64{"id": id})
	})
}
