package app

import (
	"net/http"

	"video-rename/internal/domain"
	"video-rename/internal/models"
	"video-rename/internal/systemsettings"
)

// systemStatus reports whether runs can start. Problem explains why not.
type systemStatus struct {
	RootID  int           `json:"root_id"`
	Model   string        `json:"model"`
	Ready   bool          `json:"ready"`
	Problem domain.Record `json:"problem,omitzero"`
}

func (a *App) systemSettingsRoutes(mux *router) {
	mux.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		_, _, err := a.Jobs.Tools()
		var problem domain.Record
		if err != nil {
			problem = domain.RecordOf(err)
		}
		model := ""
		configured := false
		store := models.Store{DB: a.DB}
		profiles, modelErr := store.List(r.Context())
		if modelErr != nil {
			writeError(w, r, modelErr)
			return
		}
		for _, p := range profiles {
			if p.Ready {
				configured = true
			}
			if p.Default {
				model = p.Model
			}
		}
		if !configured && err == nil {
			problem = domain.Record{Code: "no_ready_model", Message: "no model configuration is complete"}
		}
		send(w, http.StatusOK, systemStatus{RootID: 1, Model: model, Ready: err == nil && configured, Problem: problem})
	})
	store := systemsettings.Store{DB: a.DB}
	mux.HandleFunc("GET /api/system/settings", func(w http.ResponseWriter, r *http.Request) {
		s, err := store.Get(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, map[string]any{"settings": s, "cache": a.Jobs.Cache.Stats(), "retention_days": float64(s.IdleCacheHours) / 24})
	})
	mux.HandleFunc("PUT /api/system/settings", func(w http.ResponseWriter, r *http.Request) {
		s := systemsettings.Default()
		s.ExtractWorkers, s.CacheGB = 0, 0
		if !decode(w, r, &s) {
			return
		}
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		if s.FallbackModelID > 0 {
			p, err := (models.Store{DB: a.DB}).Get(r.Context(), s.FallbackModelID)
			if err != nil || !p.Ready {
				invalid(w, r, "fallback_model_id", "model_not_ready")
				return
			}
		}
		if err := store.Save(r.Context(), s); err != nil {
			writeError(w, r, err)
			return
		}
		if err := a.Jobs.Cache.Configure(s.Bytes(), s.Policy); err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, s)
	})
	mux.HandleFunc("POST /api/system/cache/clear", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Confirm bool `json:"confirm"`
		}
		if !decode(w, r, &body) {
			return
		}
		if !body.Confirm {
			invalid(w, r, "confirm", "confirmation_required")
			return
		}
		result, err := a.Jobs.Cache.Clear()
		if err != nil {
			writeError(w, r, domain.Fail(domain.KindInternal, "cache_clear_incomplete", "cache clear stopped", "deleted", result.Deleted).Wrap(err))
			return
		}
		send(w, http.StatusOK, result)
	})
}
