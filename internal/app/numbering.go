package app

import (
	"net/http"
	"video-rename/internal/domain"
	"video-rename/internal/systemsettings"
)

// numberingRoutes serves both media classes; the batch journal records which
// class a numbering belongs to, so apply/restore/recover only need the ID.
// Previews always read the currently saved rules; confirmed batches replay
// the names frozen at preview time, so later rule changes never mutate them.
func (a *App) numberingRoutes(api *http.ServeMux) {
	store := systemsettings.Store{DB: a.DB}
	api.HandleFunc("POST /api/directories/{id}/numberings/tree-preview", func(w http.ResponseWriter, r *http.Request) {
		settings, err := store.Numbering()
		if err != nil {
			failure(w, err)
			return
		}
		batches, err := a.Library.PreviewNumberingTree(r.Context(), number(r.PathValue("id")), settings)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, batches)
	})
	api.HandleFunc("GET /api/directories/{id}/numberings/tree", func(w http.ResponseWriter, r *http.Request) {
		batches, err := a.Library.NumberingTreeHistory(number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, batches)
	})
	api.HandleFunc("GET /api/numbering/settings", func(w http.ResponseWriter, r *http.Request) {
		s, err := store.Numbering()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]any{"settings": s})
	})
	api.HandleFunc("PUT /api/numbering/settings", func(w http.ResponseWriter, r *http.Request) {
		var s domain.NumberingSettings
		if !decode(w, r, &s) {
			return
		}
		if err := store.SaveNumbering(s); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, s)
	})
	api.HandleFunc("POST /api/directories/{id}/numberings/preview", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Media string `json:"media"`
			Query string `json:"query"`
			Sort  string `json:"sort"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.Media != "image" && v.Media != "audio" {
			send(w, 400, map[string]string{"message": "无效的媒体类型"})
			return
		}
		settings, err := store.Numbering()
		if err != nil {
			failure(w, err)
			return
		}
		b, err := a.Library.PreviewNumbering(number(r.PathValue("id")), v.Media, v.Query, v.Sort, settings)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("GET /api/directories/{id}/numberings", func(w http.ResponseWriter, r *http.Request) {
		media := r.URL.Query().Get("media")
		if media != "image" && media != "audio" {
			send(w, 400, map[string]string{"message": "无效的媒体类型"})
			return
		}
		b, err := a.Library.NumberingHistory(number(r.PathValue("id")), media)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("POST /api/numberings/{id}/apply", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.ApplyNumbering(r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("POST /api/numberings/{id}/restore-preview", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.PreviewNumberingRestore(r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("POST /api/numberings/{id}/recover", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.RecoverNumbering(r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
}
