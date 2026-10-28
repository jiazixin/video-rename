package app

import (
	"net/http"
	"strconv"

	"video-rename/internal/domain"
	"video-rename/internal/prompt"
	"video-rename/internal/systemsetting"
)

// promptRoutes serves the prompt settings tab: the global fixed prompt (a hard
// constraint appended below the selected prompt at request time) plus the
// reusable library that model configs reference by id. The list payload also
// carries the built-in default fixed prompt so the page can restore it.
func (a *App) promptRoutes(api *router) {
	store := prompt.New(a.db)
	settings := systemsetting.New(a.db)
	api.HandleFunc("GET /api/prompts", func(w http.ResponseWriter, r *http.Request) {
		fixed, err := settings.FixedPrompt(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		list, err := store.List(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		keywords, err := settings.FailureKeywords(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		send(w, http.StatusOK, map[string]any{"fixed_prompt": fixed, "default_fixed_prompt": systemsetting.DefaultFixedPrompt, "failure_keywords": keywords, "default_failure_keywords": systemsetting.DefaultFailureKeywords(), "prompts": list})
	})
	api.HandleFunc("PUT /api/prompts/failure-keywords", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			FailureKeywords *[]string `json:"failure_keywords"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.FailureKeywords == nil {
			writeError(w, r, domain.InvalidField("failure_keywords", "required"))
			return
		}
		keywords, err := settings.SaveFailureKeywords(r.Context(), *input.FailureKeywords)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, map[string]any{"failure_keywords": keywords})
	})
	api.HandleFunc("PUT /api/prompts/fixed", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			FixedPrompt string `json:"fixed_prompt"`
		}
		if !decode(w, r, &input) {
			return
		}
		fixed, err := settings.SaveFixedPrompt(r.Context(), input.FixedPrompt)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, map[string]string{"fixed_prompt": fixed})
	})
	api.HandleFunc("GET /api/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		value, err := store.Get(r.Context(), number(r.PathValue("id")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, value)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		id, ok := creationOrUpdateID(w, r)
		if !ok {
			return
		}
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
			input.Title, input.Body, version = current.Title, current.Body, current.Version
			if !decodePatch(w, r, &input) {
				return
			}
		} else if !decode(w, r, &input) {
			return
		}
		out, err := store.Save(r.Context(), prompt.Profile{ID: id, Version: version, Title: input.Title, Body: input.Body})
		if err != nil {
			writeError(w, r, err)
			return
		}
		if id == 0 {
			w.Header().Set("Location", "/api/prompts/"+strconv.FormatInt(out.ID, 10))
		}
		send(w, creationStatus(r), out)
	}
	api.HandleFunc("POST /api/prompts", save)
	api.HandleFunc("PATCH /api/prompts/{id}", save)
	api.HandleFunc("DELETE /api/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
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
}
