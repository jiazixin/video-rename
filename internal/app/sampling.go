package app

import (
	"net/http"
	"video-rename/internal/config"
	"video-rename/internal/sampling"
)

func (a *App) samplingRoutes(mux *http.ServeMux) {
	store := sampling.Store{DB: a.DB}
	mux.HandleFunc("GET /api/sampling", func(w http.ResponseWriter, r *http.Request) {
		value, err := store.Get()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, value)
	})
	mux.HandleFunc("PUT /api/sampling", func(w http.ResponseWriter, r *http.Request) {
		var value config.FrameSettings
		if !decode(w, r, &value) {
			return
		}
		if err := store.Save(value); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, value)
	})
}
