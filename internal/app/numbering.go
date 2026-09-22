package app

import "net/http"

func (a *App) numberingRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/directories/{id}/image-numberings/preview", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Query string `json:"query"`
			Sort  string `json:"sort"`
		}
		if !decode(w, r, &v) {
			return
		}
		b, err := a.Library.PreviewNumbering(number(r.PathValue("id")), v.Query, v.Sort)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("GET /api/directories/{id}/image-numberings", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.NumberingHistory(number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("POST /api/image-numberings/{id}/apply", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.ApplyNumbering(r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("POST /api/image-numberings/{id}/restore-preview", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.PreviewNumberingRestore(r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
	api.HandleFunc("POST /api/image-numberings/{id}/recover", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.RecoverNumbering(r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, b)
	})
}
