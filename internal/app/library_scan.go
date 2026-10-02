package app

import (
	"net/http"

	"video-rename/internal/systemsettings"
)

func (a *App) libraryScanRoutes(mux *http.ServeMux) {
	store := systemsettings.Store{DB: a.DB}
	mux.HandleFunc("GET /api/library/scan", func(w http.ResponseWriter, r *http.Request) {
		settings, err := store.Scan()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, http.StatusOK, map[string]any{"settings": settings, "status": a.Scanner.Snapshot()})
	})
	mux.HandleFunc("PUT /api/library/scan/settings", func(w http.ResponseWriter, r *http.Request) {
		var settings systemsettings.ScanSettings
		if !decode(w, r, &settings) {
			return
		}
		if err := settings.Validate(); err != nil {
			failure(w, err)
			return
		}
		targets, err := a.Scanner.ValidateTargets(r.Context(), settings.Targets)
		if err != nil {
			failure(w, err)
			return
		}
		settings.Targets = targets
		a.settingsMu.Lock()
		err = store.SaveScan(settings)
		a.settingsMu.Unlock()
		if err != nil {
			failure(w, err)
			return
		}
		a.Scanner.SettingsChanged()
		send(w, http.StatusOK, settings)
	})
	mux.HandleFunc("POST /api/library/scan", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Targets []int64 `json:"targets"`
		}
		if !decode(w, r, &body) {
			return
		}
		status, err := a.Scanner.Start(a.ctx, body.Targets, "manual")
		if err != nil {
			failure(w, err)
			return
		}
		send(w, http.StatusAccepted, status)
	})
	mux.HandleFunc("POST /api/library/scan/cancel", func(w http.ResponseWriter, r *http.Request) {
		var body struct{}
		if !decode(w, r, &body) {
			return
		}
		send(w, http.StatusAccepted, a.Scanner.Cancel())
	})
	mux.HandleFunc("GET /api/directories/{id}/state", func(w http.ResponseWriter, r *http.Request) {
		state, err := a.Library.DirectoryState(r.Context(), number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, http.StatusOK, state)
	})
}
