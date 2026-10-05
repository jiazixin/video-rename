package app

import (
	"fmt"
	"net/http"
	"video-rename/internal/library"
)

func (a *App) libraryRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/directories/{id}/entries", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var p library.Page
		var err error
		if q.Has("page") {
			p, err = a.Library.ListPageFiltered(r.Context(), number(r.PathValue("id")), int(number(q.Get("page"))), int(number(q.Get("limit"))), q.Get("q"), q.Get("kind"), q.Get("status"), q.Get("sort"))
		} else {
			p, err = a.Library.ListOrderedFiltered(r.Context(), number(r.PathValue("id")), int(number(q.Get("offset"))), int(number(q.Get("limit"))), number(q.Get("generation")), q.Get("q"), q.Get("kind"), q.Get("status"), false, q.Get("sort"))
		}
		if err != nil {
			failure(w, err)
			return
		}
		withProgress, err := a.Library.WithProgressBatch(r.Context(), append(p.Items, p.Directory))
		if err != nil {
			failure(w, err)
			return
		}
		p.Items = withProgress[:len(withProgress)-1]
		p.Directory = withProgress[len(withProgress)-1]
		send(w, 200, p)
	})
	api.HandleFunc("POST /api/directories/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		a.Library.StartRefresh(a.ctx, number(r.PathValue("id")))
		send(w, 202, map[string]bool{"ok": true})
	})
	api.HandleFunc("DELETE /api/entries/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Version   int64 `json:"version"`
			Confirmed bool  `json:"confirmed"`
		}
		if !decode(w, r, &v) {
			return
		}
		if !v.Confirmed {
			failure(w, fmt.Errorf("请先确认永久删除"))
			return
		}
		if err := a.Library.Delete(number(r.PathValue("id")), v.Version); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("POST /api/entries/{id}/rename", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name    string `json:"name"`
			Version int64  `json:"version"`
			Key     string `json:"key"`
		}
		if !decode(w, r, &v) {
			return
		}
		id := number(r.PathValue("id"))
		o, err := a.Library.Rename(id, v.Version, v.Name, v.Key)
		if err != nil {
			_ = a.Library.Event("failure", "manual", id, 0, 0, a.Library.Relative(id), "", err.Error())
			failure(w, err)
			return
		}
		send(w, 200, o)
	})
	api.HandleFunc("GET /api/entries/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Library.History(r.Context(), number(r.PathValue("id")), number(r.URL.Query().Get("before")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	api.HandleFunc("POST /api/operations/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Key string `json:"key"`
		}
		if !decode(w, r, &v) {
			return
		}
		o, err := a.Library.Restore(number(r.PathValue("id")), v.Key)
		if err != nil {
			_ = a.Library.Event("failure", "restore", 0, number(r.PathValue("id")), 0, "", "", err.Error())
			failure(w, err)
			return
		}
		send(w, 200, o)
	})
}
