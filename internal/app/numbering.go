package app

import (
	"context"
	"errors"
	"maps"
	"net/http"

	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/systemsettings"
)

// numberingRoutes serves both media classes; the batch journal records which
// class a numbering belongs to, so apply/restore/recover only need the ID.
// Previews always read the currently saved rules; confirmed batches replay
// the names frozen at preview time, so later rule changes never mutate them.
func (a *App) numberingRoutes(api *router) {
	a.numberingJobRoutes(api)
	store := systemsettings.Store{DB: a.DB}
	api.HandleFunc("POST /api/directories/{id}/numberings/tree-preview", func(w http.ResponseWriter, r *http.Request) {
		settings, err := store.Numbering()
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		batches, err := a.Library.PreviewNumberingTree(r.Context(), number(r.PathValue("id")), settings)
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, batches)
	})
	api.HandleFunc("GET /api/directories/{id}/numberings/tree", func(w http.ResponseWriter, r *http.Request) {
		batches, err := a.Library.NumberingTreeHistory(number(r.PathValue("id")))
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, batches)
	})
	api.HandleFunc("GET /api/numbering/settings", func(w http.ResponseWriter, r *http.Request) {
		s, err := store.Numbering()
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, map[string]any{"settings": s})
	})
	api.HandleFunc("PUT /api/numbering/settings", func(w http.ResponseWriter, r *http.Request) {
		var s domain.NumberingSettings
		if !decode(w, r, &s) {
			return
		}
		if err := store.SaveNumbering(s); err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, s)
	})
	api.HandleFunc("POST /api/directories/{id}/numberings/preview", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Media  string `json:"media"`
			Query  string `json:"query"`
			Status string `json:"status"`
			Sort   string `json:"sort"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.Media != "image" && v.Media != "audio" {
			invalid(w, r, "media", "invalid_choice")
			return
		}
		settings, err := store.Numbering()
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		b, err := a.Library.PreviewNumbering(r.Context(), library.NumberingRequest{
			Parent:   number(r.PathValue("id")),
			Media:    v.Media,
			Query:    v.Query,
			Status:   v.Status,
			Sort:     v.Sort,
			Settings: settings,
		})
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, b)
	})
	api.HandleFunc("GET /api/directories/{id}/numberings", func(w http.ResponseWriter, r *http.Request) {
		media := r.URL.Query().Get("media")
		if media != "image" && media != "audio" {
			invalid(w, r, "media", "invalid_choice")
			return
		}
		b, err := a.Library.NumberingHistory(number(r.PathValue("id")), media)
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, b)
	})
	api.HandleFunc("POST /api/numberings/{id}/apply", func(w http.ResponseWriter, r *http.Request) {
		// Renames must not stop half way when the client disconnects.
		b, err := a.Library.ApplyNumbering(context.WithoutCancel(r.Context()), r.PathValue("id"), nil)
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, b)
	})
	api.HandleFunc("POST /api/numberings/{id}/restore-preview", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.PreviewNumberingRestore(r.Context(), r.PathValue("id"))
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, b)
	})
	api.HandleFunc("POST /api/numberings/{id}/recover", func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Library.RecoverNumbering(r.PathValue("id"))
		if err != nil {
			numberingFailure(w, r, err)
			return
		}
		send(w, http.StatusOK, b)
	})
}

// Numbering preflight errors are returned before a batch is saved. Preserve
// wrapped filesystem diagnostics in the response and the service error output.
func numberingFailure(w http.ResponseWriter, r *http.Request, err error) {
	if diagnostic, ok := errors.AsType[*domain.JobError](err); ok && diagnostic.ProcessingLog != "" {
		requestLogger(r).Warn("numbering_failed", "error", err, "diagnostic", diagnostic.ProcessingLog)
		status, body := errorResponse(r, err)
		body.Details = maps.Clone(body.Details)
		if body.Details == nil {
			body.Details = map[string]any{}
		}
		body.Details["processing_log"] = diagnostic.ProcessingLog
		send(w, status, map[string]any{"error": body})
		return
	}
	requestLogger(r).Warn("numbering_failed", "error", err)
	writeError(w, r, err)
}
