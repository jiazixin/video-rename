package app

import (
	"net/http"

	"video-rename/internal/prompts"
	"video-rename/internal/systemsettings"
)

// promptRoutes serves the prompt settings tab: the global fixed prompt (a hard
// constraint appended below the selected prompt at request time) plus the
// reusable library that model configs reference by id. The list payload also
// carries the built-in default fixed prompt so the page can restore it.
func (a *App) promptRoutes(api *router) {
	store := prompts.Store{DB: a.DB}
	settings := systemsettings.Store{DB: a.DB}
	api.HandleFunc("GET /api/prompts", func(w http.ResponseWriter, r *http.Request) {
		fixed, err := settings.FixedPrompt()
		if err != nil {
			writeError(w, r, err)
			return
		}
		list, err := store.List()
		if err != nil {
			writeError(w, r, err)
			return
		}
		keywords, err := settings.FailureKeywords()
		if err != nil {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		send(w, http.StatusOK, map[string]any{"fixed_prompt": fixed, "default_fixed_prompt": systemsettings.DefaultFixedPrompt, "failure_keywords": keywords, "default_failure_keywords": systemsettings.DefaultFailureKeywords(), "prompts": list})
	})
	api.HandleFunc("PUT /api/prompts/failure-keywords", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			FailureKeywords *[]string `json:"failure_keywords"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.FailureKeywords == nil {
			invalid(w, r, "failure_keywords", "required")
			return
		}
		keywords, err := settings.SaveFailureKeywords(*input.FailureKeywords)
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
		fixed, err := settings.SaveFixedPrompt(input.FixedPrompt)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, map[string]string{"fixed_prompt": fixed})
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var p prompts.Profile
		if !decode(w, r, &p) {
			return
		}
		id, ok := creationOrUpdateID(w, r)
		if !ok {
			return
		}
		p.ID = id
		out, err := store.Save(p)
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, out)
	}
	api.HandleFunc("POST /api/prompts", save)
	api.HandleFunc("PATCH /api/prompts/{id}", save)
	api.HandleFunc("DELETE /api/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Version int64 `json:"version"`
		}
		if !decode(w, r, &input) {
			return
		}
		if err := store.Delete(number(r.PathValue("id")), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
