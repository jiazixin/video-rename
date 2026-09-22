package app

import (
	"net/http"
	"video-rename/internal/systemsettings"
)

func (a *App) systemSettingsRoutes(mux *http.ServeMux) {
	store := systemsettings.Store{DB: a.DB}
	mux.HandleFunc("GET /api/system/settings", func(w http.ResponseWriter, r *http.Request) {
		s, err := store.Get()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]any{"settings": s, "cache": a.Jobs.Cache.Stats(), "retention_days": 3})
	})
	mux.HandleFunc("PUT /api/system/settings", func(w http.ResponseWriter, r *http.Request) {
		s := systemsettings.Settings{StabilitySeconds: 30}
		if !decode(w, r, &s) {
			return
		}
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		if err := store.Save(s); err != nil {
			failure(w, err)
			return
		}
		if err := a.Jobs.Cache.SetLimit(s.Bytes()); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, s)
	})
}
