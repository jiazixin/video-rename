package app

import (
	"net/http"
	"strconv"
	"time"
	_ "time/tzdata"
	"video-rename/internal/notifications"
)

func (a *App) notificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/notifications/timezone", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Timezone string `json:"timezone"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Timezone == "" || input.Timezone == "Local" || len(input.Timezone) > 100 {
			send(w, 400, map[string]string{"message": "无效时区"})
			return
		}
		if _, err := time.LoadLocation(input.Timezone); err != nil {
			send(w, 400, map[string]string{"message": "无效时区"})
			return
		}
		if _, err := a.DB.Exec("UPDATE notification_preferences SET timezone=? WHERE id=1 AND timezone<>?", input.Timezone, input.Timezone); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Notifications.List()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	mux.HandleFunc("PUT /api/notifications/{kind}", func(w http.ResponseWriter, r *http.Request) {
		var v notifications.Input
		if !decode(w, r, &v) {
			return
		}
		v.Kind = r.PathValue("kind")
		if err := a.Notifications.Save(v); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/notifications/{kind}/test", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Notifications.Test(r.PathValue("kind")); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]string{"message": "测试通知已发送"})
	})
	mux.HandleFunc("GET /api/notifications/history", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("page") || r.URL.Query().Has("size") {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			size, _ := strconv.Atoi(r.URL.Query().Get("size"))
			v, err := a.Notifications.ListHistoryPage(r.Context(), page, size)
			if err != nil {
				failure(w, err)
				return
			}
			send(w, 200, v)
			return
		}
		v, err := a.Notifications.History()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
}
