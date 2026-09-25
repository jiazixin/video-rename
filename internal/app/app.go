// Package app assembles the HTTP service. Domain logic lives in its owning package.
package app

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"video-rename/internal/auth"
	"video-rename/internal/config"
	"video-rename/internal/jobs"
	"video-rename/internal/library"
	"video-rename/internal/models"
	"video-rename/internal/notifications"
	"video-rename/internal/scheduler"
	"video-rename/internal/storage"
	webassets "video-rename/web"
)

type App struct {
	Notifications *notifications.Service
	DB            *storage.Store
	Library       *library.Library
	Jobs          *jobs.Manager
	Scheduler     *scheduler.Scheduler
	Auth          *auth.Auth
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	settingsMu    sync.Mutex
}

func New(cfg *config.Config) (*App, error) {
	db, err := storage.Open(cfg.DatabasePath())
	if err != nil {
		return nil, err
	}
	l, err := library.New(db, cfg.Path)
	if err != nil {
		db.Close()
		return nil, err
	}
	j, err := jobs.New(db, l, cfg)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &scheduler.Scheduler{DB: db, Jobs: j}
	if err = s.ResetNext(time.Now()); err != nil {
		j.Cache.Close()
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &App{Notifications: notifications.New(db), DB: db, Library: l, Jobs: j, Scheduler: s, Auth: auth.New(db, cfg.Auth), ctx: ctx, cancel: cancel}, nil
}
func (a *App) Start() {
	a.wg.Go(func() { a.Notifications.Loop(a.ctx) })
	a.wg.Go(func() { a.Jobs.Loop(a.ctx) })
	a.wg.Go(func() { a.Scheduler.Loop(a.ctx) })
}
func (a *App) Close() error {
	a.cancel()
	a.wg.Wait()
	a.Library.Close()
	a.Jobs.Cache.Close()
	return a.DB.Close()
}
func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v)
}
func failure(w http.ResponseWriter, err error) {
	status := 409
	code := "CONFLICT"
	if errors.Is(err, sql.ErrNoRows) {
		status = 404
		code = "NOT_FOUND"
	}
	send(w, status, map[string]string{"code": code, "message": err.Error(), "request_id": auth.Token()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	d := jsontext.NewDecoder(r.Body)
	if err := json.UnmarshalDecode(d, v, json.RejectUnknownMembers(true)); err != nil {
		send(w, 400, map[string]string{"message": "请求字段或格式无效"})
		return false
	}
	if _, err := d.ReadValue(); !errors.Is(err, io.EOF) {
		send(w, 400, map[string]string{"message": "请求只能包含一个 JSON 对象"})
		return false
	}
	return true
}
func number(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
func (a *App) Handler() http.Handler {
	api := http.NewServeMux()
	a.modelRoutes(api)
	a.promptRoutes(api)
	a.notificationRoutes(api)
	a.samplingRoutes(api)
	a.systemSettingsRoutes(api)
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
	api.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		_, _, err := a.Jobs.Tools()
		message := ""
		if err != nil {
			message = err.Error()
		}
		model := ""
		configured := false
		store := models.Store{DB: a.DB}
		profiles, modelErr := store.List()
		if modelErr != nil {
			failure(w, modelErr)
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
		if !configured && message == "" {
			message = "请先在模型配置页面完善一个模型"
		}
		send(w, 200, map[string]any{"root_id": 1, "model": model, "ready": err == nil && configured, "message": message})
	})
	api.HandleFunc("GET /api/directories/{id}/entries", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, err := a.Library.ListOrdered(a.ctx, number(r.PathValue("id")), int(number(q.Get("offset"))), int(number(q.Get("limit"))), number(q.Get("generation")), q.Get("q"), q.Get("kind"), false, q.Get("sort"))
		if err != nil {
			failure(w, err)
			return
		}
		for i, e := range p.Items {
			p.Items[i], err = a.Library.WithProgress(e)
			if err != nil {
				failure(w, err)
				return
			}
		}
		p.Directory, err = a.Library.WithProgress(p.Directory)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, p)
	})
	api.HandleFunc("POST /api/directories/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		a.Library.StartRefresh(a.ctx, number(r.PathValue("id")))
		send(w, 202, map[string]bool{"ok": true})
	})
	api.HandleFunc("POST /api/entries/{id}/rename", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name    string `json:"name"`
			Version int64  `json:"version"`
			Key     string `json:"key"`
		}
		if !decode(w, r, &v) {
			return
		}
		id := number(r.PathValue("id"))
		o, err := a.Library.Rename(id, v.Version, v.Name, v.Key)
		if err != nil {
			_ = a.Library.Event("failure", "manual", id, 0, 0, a.Library.Relative(id), "", err.Error())
			failure(w, err)
			return
		}
		send(w, 200, o)
	})
	api.HandleFunc("GET /api/entries/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Library.History(number(r.PathValue("id")), number(r.URL.Query().Get("before")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	api.HandleFunc("POST /api/operations/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Key string `json:"key"`
		}
		if !decode(w, r, &v) {
			return
		}
		o, err := a.Library.Restore(number(r.PathValue("id")), v.Key)
		if err != nil {
			_ = a.Library.Event("failure", "restore", 0, number(r.PathValue("id")), 0, "", "", err.Error())
			failure(w, err)
			return
		}
		send(w, 200, o)
	})
	api.HandleFunc("GET /api/logs", a.logs)
	api.HandleFunc("GET /api/logs/batches", a.logBatches)
	api.HandleFunc("GET /api/usage/analytics", a.usageAnalytics)
	api.HandleFunc("GET /api/runs/analytics", a.runsAnalytics)
	api.HandleFunc("POST /api/runs", func(w http.ResponseWriter, r *http.Request) {
		var v jobs.Request
		if !decode(w, r, &v) {
			return
		}
		id, err := a.Jobs.Create(v, "manual", 0)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 202, map[string]int64{"id": id})
	})
	api.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		if category := r.URL.Query().Get("category"); category != "" {
			if category != "manual" && category != "automatic" {
				send(w, 400, map[string]string{"message": "运行分类无效"})
				return
			}
			v, err := a.Jobs.ListPage(r.Context(), category, int(number(r.URL.Query().Get("page"))), int(number(r.URL.Query().Get("size"))))
			if err != nil {
				failure(w, err)
				return
			}
			send(w, 200, v)
			return
		}
		v, err := a.Jobs.List(number(r.URL.Query().Get("before")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	api.HandleFunc("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Jobs.Get(number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	api.HandleFunc("GET /api/runs/{id}/items", a.items)
	api.HandleFunc("POST /api/runs/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		result, err := a.Library.RestoreBatch(r.Context(), number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, result)
	})
	api.HandleFunc("POST /api/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Jobs.Cancel(number(r.PathValue("id"))); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Scheduler.List()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var p scheduler.Plan
		if !decode(w, r, &p) {
			return
		}
		p.ID = number(r.PathValue("id"))
		v, err := a.Scheduler.Save(p)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, v)
	}
	api.HandleFunc("POST /api/schedules", save)
	api.HandleFunc("PATCH /api/schedules/{id}", save)
	api.HandleFunc("DELETE /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Scheduler.Delete(number(r.PathValue("id"))); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("POST /api/schedules/preview", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Expression string `json:"expression"`
			Timezone   string `json:"timezone"`
		}
		if !decode(w, r, &v) {
			return
		}
		out, err := scheduler.Preview(v.Expression, v.Timezone, time.Now())
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, out)
	})
	api.HandleFunc("POST /api/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Key string `json:"key"`
		}
		if !decode(w, r, &v) {
			return
		}
		p, err := a.Scheduler.Get(number(r.PathValue("id")))
		if err != nil {
			failure(w, err)
			return
		}
		id, err := a.Jobs.Create(jobs.Request{ModelConfigID: p.ModelConfigID, Targets: p.Targets, Recursive: p.Recursive, Key: v.Key}, "manual", p.ID)
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 202, map[string]int64{"id": id})
	})
	mux := http.NewServeMux()
	mux.Handle("/api/", a.Auth.Middleware(api))
	assets, _ := fs.Sub(webassets.Assets, "dist")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(assets, name); err != nil {
			if strings.Contains(name, ".") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if name == "index.html" {
			data, err := fs.ReadFile(assets, name)
			if err != nil {
				http.Error(w, "资源不可用", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(data)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + name
		http.FileServer(http.FS(assets)).ServeHTTP(w, r2)
	})
	return mux
}
func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	category := q.Get("category")
	if category != "success" && category != "failure" {
		send(w, 400, map[string]string{"message": "请选择成功或失败日志"})
		return
	}
	run := int64(0)
	if q.Get("run") != "" {
		var err error
		run, err = strconv.ParseInt(q.Get("run"), 10, 64)
		if err != nil || run <= 0 {
			send(w, 400, map[string]string{"message": "请选择有效的运行批次"})
			return
		}
	}
	before := number(q.Get("before"))
	if before <= 0 {
		before = 1 << 62
	}
	limit := number(q.Get("limit"))
	if limit != 50 && limit != 100 && limit != 200 {
		limit = 100
	}
	where := " FROM events e LEFT JOIN runs r ON r.id=e.run WHERE e.category=? AND (?=0 OR e.run=?) AND (?='' OR instr(lower(e.old_path||e.new_path||e.message),lower(?))>0) AND (?='' OR e.action=?) AND (?='' OR e.created>=?) AND (?='' OR e.created<=?)"
	args := []any{category, run, run, q.Get("q"), q.Get("q"), q.Get("action"), q.Get("action"), q.Get("from"), q.Get("from"), q.Get("to"), q.Get("to")}
	var total int
	if err := a.DB.QueryRow("SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		failure(w, err)
		return
	}
	rows, err := a.DB.Query("SELECT e.id,e.category,e.action,e.entry,e.operation,e.run,e.old_path,e.new_path,e.message,e.created,COALESCE(r.config_summary,''),COALESCE(r.model_config_title,''),COALESCE((SELECT SUM(t.tokens) FROM token_usage_records t JOIN run_items i ON i.id=t.item WHERE t.run=e.run AND i.entry=e.entry),0),COALESCE((SELECT MAX(t.estimated_records) FROM token_usage_records t JOIN run_items i ON i.id=t.item WHERE t.run=e.run AND i.entry=e.entry),0)"+where+" AND e.id<? ORDER BY e.id DESC LIMIT ?", append(args, before, limit+1)...)
	if err != nil {
		failure(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, entry, op, run int64
		var cat, action, old, new, msg, created, summary, modelTitle string
		var tokens, tokenEstimated int64
		if err = rows.Scan(&id, &cat, &action, &entry, &op, &run, &old, &new, &msg, &created, &summary, &modelTitle, &tokens, &tokenEstimated); err != nil {
			failure(w, err)
			return
		}
		var snapshot struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal([]byte(summary), &snapshot)
		out = append(out, map[string]any{"id": id, "category": cat, "action": action, "entry": entry, "operation": op, "run": run, "old_path": old, "new_path": new, "message": msg, "created": created, "model": snapshot.Model, "model_config_title": modelTitle, "tokens": tokens, "token_estimated": tokenEstimated > 0})
	}
	if err := rows.Err(); err != nil {
		failure(w, err)
		return
	}
	next := int64(0)
	if int64(len(out)) > limit {
		out = out[:limit]
		next = out[len(out)-1]["id"].(int64)
	}
	if q.Get("paged") == "1" {
		send(w, 200, map[string]any{"items": out, "total": total, "next": next})
		return
	}
	send(w, 200, out)
}
func (a *App) items(w http.ResponseWriter, r *http.Request) {
	after := number(r.URL.Query().Get("after"))
	rows, err := a.DB.Query("SELECT id,entry,status,message,original_path FROM run_items WHERE run=? AND id>? ORDER BY id LIMIT 100", number(r.PathValue("id")), after)
	if err != nil {
		failure(w, err)
		return
	}
	out := []map[string]any{}
	for rows.Next() {
		var id, entry int64
		var status, message, original string
		if err = rows.Scan(&id, &entry, &status, &message, &original); err != nil {
			break
		}
		out = append(out, map[string]any{"id": id, "entry": entry, "status": status, "message": message, "original_path": original})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		failure(w, err)
		return
	}
	for _, item := range out {
		item["current_path"] = a.Library.DisplayPath(item["entry"].(int64))
	}
	send(w, 200, out)
}
func Serve(ctx context.Context, cfg *config.Config) error {
	a, err := New(cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	srv := &http.Server{Addr: net.JoinHostPort(cfg.Listen, strconv.Itoa(cfg.Port)), Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("http listen failed: %w", err)
	}
	defer listener.Close()
	a.Start()
	slog.Info("http server started", "listen", cfg.Listen, "port", cfg.Port)
	slog.Info("scheduler started")
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
