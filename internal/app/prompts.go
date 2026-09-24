package app

import (
	"net/http"
	"video-rename/internal/prompts"
	"video-rename/internal/systemsettings"
)

// promptRoutes serves the Prompt配置 tab: the global fixed prompt (a hard
// constraint appended below the selected prompt at request time) plus the
// reusable library that model configs reference by id. The list payload also
// carries the built-in default fixed prompt so the page can restore it.
func (a *App) promptRoutes(api *http.ServeMux) {
	store := prompts.Store{DB: a.DB}
	settings := systemsettings.Store{DB: a.DB}
	api.HandleFunc("GET /api/prompts", func(w http.ResponseWriter, r *http.Request) {
		fixed, err := settings.FixedPrompt()
		if err != nil {
			failure(w, err)
			return
		}
		list, err := store.List()
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		send(w, 200, map[string]any{"fixed_prompt": fixed, "default_fixed_prompt": systemsettings.DefaultFixedPrompt, "prompts": list})
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
			failure(w, err)
			return
		}
		send(w, 200, map[string]string{"fixed_prompt": fixed})
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var p prompts.Profile
		if !decode(w, r, &p) {
			return
		}
		p.ID = number(r.PathValue("id"))
		out, err := store.Save(p)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, out)
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
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
}
