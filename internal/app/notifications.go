package app

import (
	"net/http"
	"time"
	_ "time/tzdata"

	"video-rename/internal/domain"
	"video-rename/internal/notification"
)

func (a *App) notificationRoutes(mux *router) {
	mux.HandleFunc("PUT /api/notifications/timezone", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Timezone string `json:"timezone"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Timezone == "" || input.Timezone == "Local" || len(input.Timezone) > 100 {
			writeError(w, r, domain.InvalidField("timezone", "invalid_timezone"))
			return
		}
		if _, err := time.LoadLocation(input.Timezone); err != nil {
			writeError(w, r, domain.InvalidField("timezone", "invalid_timezone"))
			return
		}
		if err := a.Notifications.SetTimezone(r.Context(), input.Timezone); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Notifications.List(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, v)
	})
	mux.HandleFunc("PUT /api/notifications/{kind}", func(w http.ResponseWriter, r *http.Request) {
		var v notification.Input
		if !decode(w, r, &v) {
			return
		}
		v.Kind = r.PathValue("kind")
		if err := a.Notifications.Save(r.Context(), v); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/notifications/{kind}/test", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Notifications.Test(r.Context(), r.PathValue("kind")); err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, struct{}{})
	})
	mux.HandleFunc("GET /api/notifications/history", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("page") || r.URL.Query().Has("size") {
			page := int(number(r.URL.Query().Get("page")))
			size := int(number(r.URL.Query().Get("size")))
			v, err := a.Notifications.ListHistoryPage(r.Context(), page, size)
			if err != nil {
				writeError(w, r, err)
				return
			}
			send(w, http.StatusOK, v)
			return
		}
		v, err := a.Notifications.History(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		send(w, http.StatusOK, v)
	})
}
