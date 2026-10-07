package app

import (
	"fmt"
	"net/http"
	"slices"

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
		paths, err := a.Library.DisplayPaths(r.Context(), append(slices.Clone(settings.Targets), settings.ExcludedTargets...))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, http.StatusOK, map[string]any{"settings": settings, "status": a.Scanner.Snapshot(), "directory_paths": paths})
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
		if len(settings.ExcludedTargets) > 0 {
			excluded, err := a.Scanner.ValidateTargets(r.Context(), settings.ExcludedTargets)
			if err != nil {
				failure(w, fmt.Errorf("排除目录无效：%w", err))
				return
			}
			settings.ExcludedTargets = excluded
		}
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
