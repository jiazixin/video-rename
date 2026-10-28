package app

import (
	"net/http"

	"video-rename/internal/domain"
	"video-rename/internal/library"
)

func (a *App) libraryRoutes(api *router) {
	api.HandleFunc("GET /api/directories/{id}/entries", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := queryFields(q, "offset", "limit", "q", "kind", "status", "sort"); err != nil {
			writeError(w, r, err)
			return
		}
		p, err := a.Library.ListPage(r.Context(), library.ListPageOptions{ID: number(r.PathValue("id")), Offset: int(number(q.Get("offset"))), Limit: int(number(q.Get("limit"))), Query: q.Get("q"), Kind: q.Get("kind"), Status: q.Get("status"), SortBy: q.Get("sort")})
		if err != nil {
			writeError(w, r, err)
			return
		}
		withProgress, err := a.Library.WithProgressBatch(r.Context(), append(p.Items, p.Directory))
		if err != nil {
			writeError(w, r, err)
			return
		}
		p.Items = withProgress[:len(withProgress)-1]
		p.Directory = withProgress[len(withProgress)-1]
		send(w, http.StatusOK, p)
	})
	api.HandleFunc("POST /api/directories/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		a.Library.StartRefresh(r.Context(), number(r.PathValue("id")))
		w.WriteHeader(http.StatusAccepted)
	})
	api.HandleFunc("DELETE /api/entries/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Confirmed bool `json:"confirmed"`
		}
		if !decode(w, r, &v) {
			return
		}
		if !v.Confirmed {
			writeError(w, r, domain.InvalidField("confirmed", "confirmation_required"))
			return
		}
		id := number(r.PathValue("id"))
		current, err := a.Library.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !matchVersion(w, r, current.Version) {
			return
		}
		if err := a.Library.Delete(r.Context(), id, current.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("POST /api/entries/{id}/rename", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &v) {
			return
		}
		id := number(r.PathValue("id"))
		current, err := a.Library.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !matchVersion(w, r, current.Version) {
			return
		}
		o, err := a.Library.Rename(r.Context(), library.RenameRequest{ID: id, Version: current.Version, Name: v.Name, Key: requestKey(r)})
		if err != nil {
			if recordErr := a.Library.Event(r.Context(), library.EventInput{Category: "failure", Action: "manual", Entry: id, Operation: 0, Run: 0, Old: a.Library.Relative(r.Context(), id), New: "", Message: domain.RecordText(err)}); recordErr != nil {
				requestLogger(r).Error("record_operation_failure", "error", recordErr)
			}
			writeError(w, r, err)
			return
		}
		send(w, http.StatusCreated, o)
	})
	api.HandleFunc("GET /api/entries/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		if err := queryFields(r.URL.Query(), "cursor"); err != nil {
			writeError(w, r, err)
			return
		}
		v, err := a.Library.History(r.Context(), number(r.PathValue("id")), r.URL.Query().Get("cursor"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, v)
	})
	api.HandleFunc("POST /api/operations/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		if !decodeEmpty(w, r) {
			return
		}
		o, err := a.Library.Restore(r.Context(), number(r.PathValue("id")), requestKey(r))
		if err != nil {
			if recordErr := a.Library.Event(r.Context(), library.EventInput{Category: "failure", Action: "restore", Entry: 0, Operation: number(r.PathValue("id")), Run: 0, Old: "", New: "", Message: domain.RecordText(err)}); recordErr != nil {
				requestLogger(r).Error("record_operation_failure", "error", recordErr)
			}
			writeError(w, r, err)
			return
		}
		send(w, http.StatusCreated, o)
	})
}
