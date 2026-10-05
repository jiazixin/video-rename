package app

import (
	"net/http"
)

func (a *App) Handler() http.Handler {
	api := http.NewServeMux()
	a.modelRoutes(api)
	a.promptRoutes(api)
	a.notificationRoutes(api)
	a.samplingRoutes(api)
	a.systemSettingsRoutes(api)
	a.libraryScanRoutes(api)
	a.numberingRoutes(api)
	api.HandleFunc("GET /api/logs/{id}/diagnostics", a.logDiagnostics)
	api.HandleFunc("GET /api/extractions", a.extractions)
	api.HandleFunc("GET /api/extractions/{id}", a.extractionDetail)
	api.HandleFunc("POST /api/extractions/clear", a.clearExtractions)
	api.HandleFunc("POST /api/logs/clear", a.clearHistory)
	api.HandleFunc("POST /api/logs/retry", a.retryHistory)
	api.HandleFunc("GET /api/entries/{id}/video", a.playback)
	api.HandleFunc("GET /api/entries/{id}/audio", a.audioPlayback)
	api.HandleFunc("GET /api/entries/{id}/image", a.imagePreview)
	api.HandleFunc("GET /api/entries/{id}/image-neighbor", a.adjacentImage)
	api.HandleFunc("GET /api/entries/status", a.entryStatus)
	api.HandleFunc("POST /api/entries/processed", a.markEntries)
	api.HandleFunc("GET /api/entries/processed/status/{key}", a.markStatus)
	a.libraryRoutes(api)
	a.runRoutes(api)
	a.scheduleRoutes(api)
	api.HandleFunc("GET /api/logs", a.logs)
	api.HandleFunc("GET /api/logs/batches", a.logBatches)
	api.HandleFunc("GET /api/usage/analytics", a.usageAnalytics)
	api.HandleFunc("GET /api/runs/analytics", a.runsAnalytics)
	mux := http.NewServeMux()
	mux.Handle("/api/", a.Auth.Middleware(api))
	mux.Handle("/", assetHandler())
	return mux
}
