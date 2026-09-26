package app

import (
	"net/http"
	"time"
	"video-rename/internal/models"
)

type modelInput struct {
	models.Profile
	APIKeyInput *string `json:"api_key"`
}

func (a *App) modelRoutes(api *http.ServeMux) {
	store := models.Store{DB: a.DB}
	api.HandleFunc("POST /api/model-configs/test-vision", func(w http.ResponseWriter, r *http.Request) {
		input := struct {
			modelInput
			ProbeTimeoutSeconds int `json:"probe_timeout_seconds"`
		}{ServerConfig: models.DefaultSettings(), ProbeTimeoutSeconds: 5}
		if !decode(w, r, &input) {
			return
		}
		if err := store.ResolveKey(&input.Profile, input.APIKeyInput); err != nil {
			failure(w, err)
			return
		}
		result, err := models.Probe(r.Context(), input.ServerConfig, time.Duration(input.ProbeTimeoutSeconds)*time.Second)
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		send(w, 200, result)
	})
	api.HandleFunc("POST /api/model-configs/{id}/copy", func(w http.ResponseWriter, r *http.Request) {
		p, err := store.Duplicate(number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, p)
	})
	api.HandleFunc("DELETE /api/model-configs/{id}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Version int64 `json:"version"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := store.Delete(number(r.PathValue("id")), input.Version); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/model-configs", func(w http.ResponseWriter, r *http.Request) {
		out, err := store.List()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, out)
	})
	api.HandleFunc("GET /api/model-presets", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"presets": models.Presets})
	})
	api.HandleFunc("POST /api/model-configs/{id}/default", func(w http.ResponseWriter, r *http.Request) {
		if err := store.SetDefault(number(r.PathValue("id"))); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		input := modelInput{ServerConfig: models.DefaultSettings()}
		if !decode(w, r, &input) {
			return
		}
		if input.Workers < 1 || input.Workers > 32 {
			send(w, 400, map[string]string{"message": "并发任务数须在 1–32 之间"})
			return
		}
		id, ok := creationOrUpdateID(w, r)
		if !ok {
			return
		}
		input.ID = id
		if err := store.ResolveKey(&input.Profile, input.APIKeyInput); err != nil {
			failure(w, err)
			return
		}
		out, err := store.Save(input.Profile)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, out)
	}
	api.HandleFunc("POST /api/model-configs", save)
	api.HandleFunc("PATCH /api/model-configs/{id}", save)
	api.HandleFunc("POST /api/model-configs/detect", func(w http.ResponseWriter, r *http.Request) {
		input := modelInput{ServerConfig: models.DefaultSettings()}
		if !decode(w, r, &input) {
			return
		}
		if err := store.ResolveKey(&input.Profile, input.APIKeyInput); err != nil {
			failure(w, err)
			return
		}
		out, err := models.Discover(r.Context(), input.ServerConfig, 5*time.Second)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, out)
	})
}
