package app

import (
	"net/http"

	"video-rename/internal/config"
	"video-rename/internal/sampling"
)

func (a *App) samplingRoutes(mux *router) {
	store := sampling.New(a.db)
	mux.HandleFunc("GET /api/sampling", func(w http.ResponseWriter, r *http.Request) {
		value, err := store.Get(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, value)
	})
	mux.HandleFunc("PUT /api/sampling", func(w http.ResponseWriter, r *http.Request) {
		var value config.FrameSettings
		if !decode(w, r, &value) {
			return
		}
		if err := store.Save(r.Context(), value); err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, value)
	})
}
