package app

import "net/http"

// numberingRoutes serves both media classes; the batch journal records which
// class a numbering belongs to, so apply/restore/recover only need the ID.
func (a *App) numberingRoutes(api *http.ServeMux) {
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
		b, err := a.Library.PreviewNumbering(number(r.PathValue("id")), v.Media, v.Query, v.Sort)
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
