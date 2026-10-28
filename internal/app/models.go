package app

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/model"
	"video-rename/internal/secret"
)

type modelInput struct {
	config.ServerConfig
	Title       string        `json:"title"`
	PromptID    int64         `json:"prompt_id"`
	APIKeyInput *secret.Value `json:"api_key"`
}

func (i modelInput) profile(id, version int64) model.Profile {
	return model.Profile{ServerConfig: i.ServerConfig, Title: i.Title, PromptID: i.PromptID, ID: id, Version: version}
}

type modelProbeInput struct {
	modelInput
	ID                  int64 `json:"id"`
	ProbeTimeoutSeconds int   `json:"probe_timeout_seconds"`
}

func (a *App) modelRoutes(api *router) {
	store := model.New(a.db)
	api.HandleFunc("POST /api/model-configs/test-vision", func(w http.ResponseWriter, r *http.Request) {
		input := modelProbeInput{modelInput: modelInput{ServerConfig: model.DefaultSettings()}, ProbeTimeoutSeconds: 5}
		if !decode(w, r, &input) {
			return
		}
		p := input.profile(input.ID, 0)
		if err := store.ResolveKey(r.Context(), &p, input.APIKeyInput); err != nil {
			writeError(w, r, err)
			return
		}
		result, err := model.Probe(r.Context(), p.ServerConfig, time.Duration(input.ProbeTimeoutSeconds)*time.Second)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, result)
	})
	api.HandleFunc("POST /api/model-configs/{id}/copy", func(w http.ResponseWriter, r *http.Request) {
		if !decodeEmpty(w, r) {
			return
		}
		p, err := store.Duplicate(r.Context(), number(r.PathValue("id")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Location", "/api/model-configs/"+strconv.FormatInt(p.ID, 10))
		send(w, http.StatusCreated, p)
	})
	api.HandleFunc("DELETE /api/model-configs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := number(r.PathValue("id"))
		current, err := store.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !matchVersion(w, r, current.Version) || !decodeEmpty(w, r) {
			return
		}
		if err := store.Delete(r.Context(), id, current.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("GET /api/model-configs", func(w http.ResponseWriter, r *http.Request) {
		out, err := store.List(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, out)
	})
	api.HandleFunc("GET /api/model-configs/{id}", func(w http.ResponseWriter, r *http.Request) {
		out, err := store.Get(r.Context(), number(r.PathValue("id")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, out)
	})
	api.HandleFunc("GET /api/model-presets", func(w http.ResponseWriter, r *http.Request) {
		send(w, http.StatusOK, map[string]any{"presets": model.Presets})
	})
	api.HandleFunc("POST /api/model-configs/{id}/default", func(w http.ResponseWriter, r *http.Request) {
		if !decodeEmpty(w, r) {
			return
		}
		if err := store.SetDefault(r.Context(), number(r.PathValue("id"))); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		id, ok := creationOrUpdateID(w, r)
		if !ok {
			return
		}
		input := modelInput{ServerConfig: model.DefaultSettings()}
		var version int64
		if id != 0 {
			current, err := store.Get(r.Context(), id)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if !matchVersion(w, r, current.Version) {
				return
			}
			input = modelInput{ServerConfig: current.ServerConfig, Title: current.Title, PromptID: current.PromptID}
			version = current.Version
			// A nil key means retain the secret. JSON null explicitly clears it.
			patch, valid := decodePatchMembers(w, r, &input)
			if !valid {
				return
			}
			if raw, exists := patch["api_key"]; exists && string(bytes.TrimSpace(raw)) == "null" {
				input.APIKeyInput = new(secret.Value(""))
			}
		} else if !decode(w, r, &input) {
			return
		}
		if input.Workers < 1 || input.Workers > 32 {
			writeError(w, r, domain.InvalidField("workers", "out_of_range"))
			return
		}
		p := input.profile(id, version)
		if err := store.ResolveKey(r.Context(), &p, input.APIKeyInput); err != nil {
			writeError(w, r, err)
			return
		}
		out, err := store.Save(r.Context(), p)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if id == 0 {
			w.Header().Set("Location", "/api/model-configs/"+strconv.FormatInt(out.ID, 10))
		}
		send(w, creationStatus(r), out)
	}
	api.HandleFunc("POST /api/model-configs", save)
	api.HandleFunc("PATCH /api/model-configs/{id}", save)
	api.HandleFunc("POST /api/model-configs/detect", func(w http.ResponseWriter, r *http.Request) {
		input := modelProbeInput{modelInput: modelInput{ServerConfig: model.DefaultSettings()}}
		if !decode(w, r, &input) {
			return
		}
		p := input.profile(input.ID, 0)
		if err := store.ResolveKey(r.Context(), &p, input.APIKeyInput); err != nil {
			writeError(w, r, err)
			return
		}
		out, err := model.Discover(r.Context(), p.ServerConfig, 5*time.Second)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, out)
	})
}
