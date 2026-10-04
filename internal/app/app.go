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
	"video-rename/internal/libraryscan"
	"video-rename/internal/models"
	"video-rename/internal/notifications"
	"video-rename/internal/scheduler"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
	webassets "video-rename/web"
)

type App struct {
	Notifications   *notifications.Service
	DB              *storage.Store
	Library         *library.Library
	Scanner         *libraryscan.Manager
	Jobs            *jobs.Manager
	Scheduler       *scheduler.Scheduler
	Auth            *auth.Auth
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	settingsMu      sync.Mutex
	markMu          sync.Mutex
	markJobs        map[string]*markJob
	markWG          sync.WaitGroup
	markClosed      bool
	numberingMu     sync.Mutex
	numberingJobs   map[string]*numberingJob
	numberingWG     sync.WaitGroup
	numberingClosed bool
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
	return &App{Notifications: notifications.New(db), DB: db, Library: l, Scanner: libraryscan.New(l, systemsettings.Store{DB: db}), Jobs: j, Scheduler: s, Auth: auth.New(db, cfg.Auth), ctx: ctx, cancel: cancel}, nil
}
func (a *App) Start() {
	a.wg.Go(func() { a.Notifications.Loop(a.ctx) })
	a.wg.Go(func() { a.Jobs.Loop(a.ctx) })
	a.wg.Go(func() { a.Scheduler.Loop(a.ctx) })
	a.wg.Go(func() { a.Scanner.Loop(a.ctx) })
}
func (a *App) Close() error {
	a.markMu.Lock()
	a.markClosed = true
	a.markMu.Unlock()
	a.numberingMu.Lock()
	a.numberingClosed = true
	a.numberingMu.Unlock()
	a.cancel()
	a.Scanner.Close()
	a.numberingWG.Wait()
	a.markWG.Wait()
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
	message := err.Error()
	if errors.Is(err, sql.ErrNoRows) {
		status = 404
		code = "NOT_FOUND"
		message = "请求的记录不存在或已删除，请刷新页面后重试"
	}
	send(w, status, map[string]string{"code": code, "message": message, "request_id": auth.Token()})
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

// creationOrUpdateID keeps malformed PATCH paths from becoming creates (ID 0).
func creationOrUpdateID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if r.Method == http.MethodPost {
		return 0, true
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		send(w, http.StatusBadRequest, map[string]string{"message": "记录编号无效"})
		return 0, false
	}
	return id, true
}
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
		var p library.Page
		var err error
		if q.Has("page") {
			p, err = a.Library.ListPageFiltered(a.ctx, number(r.PathValue("id")), int(number(q.Get("page"))), int(number(q.Get("limit"))), q.Get("q"), q.Get("kind"), q.Get("status"), q.Get("sort"))
		} else {
			p, err = a.Library.ListOrderedFiltered(a.ctx, number(r.PathValue("id")), int(number(q.Get("offset"))), int(number(q.Get("limit"))), number(q.Get("generation")), q.Get("q"), q.Get("kind"), q.Get("status"), false, q.Get("sort"))
		}
		if err != nil {
			failure(w, err)
			return
		}
		withProgress, err := a.Library.WithProgressBatch(r.Context(), append(p.Items, p.Directory))
		if err != nil {
			failure(w, err)
			return
		}
		p.Items = withProgress[:len(withProgress)-1]
		p.Directory = withProgress[len(withProgress)-1]
		send(w, 200, p)
	})
	api.HandleFunc("POST /api/directories/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		a.Library.StartRefresh(a.ctx, number(r.PathValue("id")))
		send(w, 202, map[string]bool{"ok": true})
	})
	api.HandleFunc("DELETE /api/entries/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Version   int64 `json:"version"`
			Confirmed bool  `json:"confirmed"`
		}
		if !decode(w, r, &v) {
			return
		}
		if !v.Confirmed {
			failure(w, fmt.Errorf("请先确认永久删除"))
			return
		}
		if err := a.Library.Delete(number(r.PathValue("id")), v.Version); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]bool{"ok": true})
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
			if category != "all" && category != "manual" && category != "automatic" {
				send(w, 400, map[string]string{"message": "运行分类无效"})
				return
			}
			task := r.URL.Query().Get("task")
			if task != "" && task != "rename" && task != "extract" {
				send(w, 400, map[string]string{"message": "任务类型无效"})
				return
			}
			v, err := a.Jobs.ListPage(r.Context(), category, task, int(number(r.URL.Query().Get("page"))), int(number(r.URL.Query().Get("size"))), r.URL.Query())
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
	for _, action := range []string{"pause", "resume"} {
		api.HandleFunc("POST /api/runs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Confirm bool `json:"confirm"`
			}
			if !decode(w, r, &request) {
				return
			}
			if !request.Confirm {
				send(w, 400, map[string]string{"message": "请确认批次操作"})
				return
			}
			id := number(r.PathValue("id"))
			if id <= 0 {
				send(w, 400, map[string]string{"message": "批次编号无效"})
				return
			}
			var err error
			if action == "pause" {
				err = a.Jobs.Pause(id)
			} else {
				err = a.Jobs.Resume(id)
			}
			if err != nil {
				failure(w, err)
				return
			}
			send(w, 200, map[string]bool{"ok": true})
		})
	}
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
		id, ok := creationOrUpdateID(w, r)
		if !ok {
			return
		}
		p.ID = id
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
		id, err := a.Jobs.Create(jobs.Request{Task: p.Task, ModelConfigID: p.ModelConfigID, Targets: p.Targets, Recursive: p.Recursive, Key: v.Key}, "manual", p.ID)
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

// Success records use the attempt that produced the title; failures use the
// last request. Match both batch and entry, and leave non-model actions alone.
// Seek by entry explicitly: otherwise SQLite can scan the whole batch through
// UNIQUE(run,identity) once for every event when statistics are absent.
const historyAttemptJoin = ` LEFT JOIN model_attempts a ON (a.item,a.attempt)=(
	SELECT ma.item,ma.attempt FROM run_items i INDEXED BY item_entry JOIN model_attempts ma ON ma.item=i.id
	WHERE i.run=e.run AND i.entry=e.entry AND e.action='automatic'
	AND (e.category='failure' OR ma.status='success')
	ORDER BY i.id DESC,ma.attempt DESC LIMIT 1)`

// Reuse the existing UNIQUE(item,attempt) index for each entry's token totals,
// rather than scanning all token records in the batch for every displayed row.
const historyTokenFrom = ` FROM run_items i INDEXED BY item_entry
	JOIN token_usage_records t INDEXED BY sqlite_autoindex_token_usage_records_1 ON t.item=i.id
	WHERE i.entry=e.entry AND t.run=e.run`

func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	category := q.Get("category")
	if category != "success" && category != "failure" {
		send(w, 400, map[string]string{"message": "请选择成功或失败日志"})
		return
	}
	modelSource := q.Get("model_source")
	if modelSource != "" && modelSource != "primary" && modelSource != "fallback" && modelSource != "unknown" {
		send(w, 400, map[string]string{"message": "请选择有效的模型来源"})
		return
	}
	for _, key := range []string{"from", "to"} {
		if value := q.Get(key); value != "" {
			stamp, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				stamp, err = time.Parse(time.DateOnly, value)
			}
			if err != nil {
				send(w, 400, map[string]string{"message": "日期格式无效"})
				return
			}
			q.Set(key, stamp.UTC().Format(time.RFC3339Nano))
		}
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
	from := " FROM events e LEFT JOIN runs r ON r.id=e.run"
	where := " WHERE e.category=? AND (?=0 OR e.run=?) AND (?='' OR instr(lower(e.old_path||e.new_path||e.message),lower(?))>0) AND (?='' OR e.action=?) AND (?='' OR julianday(e.created)>=julianday(?)) AND (?='' OR julianday(e.created)<=julianday(?))"
	args := []any{category, run, run, q.Get("q"), q.Get("q"), q.Get("action"), q.Get("action"), q.Get("from"), q.Get("from"), q.Get("to"), q.Get("to")}
	countFrom := from
	if modelSource != "" {
		countFrom += historyAttemptJoin
		where += " AND e.action='automatic'"
		if modelSource == "unknown" {
			where += " AND a.item IS NULL"
		} else {
			where += " AND a.fallback=?"
			args = append(args, modelSource == "fallback")
		}
	}
	tx, err := a.DB.Read().BeginTx(r.Context(), nil)
	if err != nil {
		failure(w, err)
		return
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*)"+countFrom+where, args...).Scan(&total); err != nil {
		failure(w, err)
		return
	}
	page, offset := int64(1), int64(0)
	if q.Has("page") {
		page = min(max(1, number(q.Get("page"))), max(1, (total+limit-1)/limit))
		offset = (page - 1) * limit
		before = 1 << 62
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT e.id,e.category,e.action,e.entry,e.operation,e.run,e.old_path,e.new_path,e.message,e.created,COALESCE(r.config_summary,''),COALESCE(a.model_config_title,r.model_config_title,''),COALESCE(a.model,''),a.fallback,COALESCE((SELECT SUM(t.tokens)"+historyTokenFrom+"),0),COALESCE((SELECT MAX(t.estimated_records)"+historyTokenFrom+"),0)"+from+historyAttemptJoin+where+" AND e.id<? ORDER BY e.id DESC LIMIT ? OFFSET ?", append(args, before, limit+1, offset)...)
	if err != nil {
		failure(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, entry, op, run int64
		var cat, action, old, new, msg, created, summary, modelTitle, attemptModel string
		var fallback sql.NullBool
		var tokens, tokenEstimated int64
		if err = rows.Scan(&id, &cat, &action, &entry, &op, &run, &old, &new, &msg, &created, &summary, &modelTitle, &attemptModel, &fallback, &tokens, &tokenEstimated); err != nil {
			failure(w, err)
			return
		}
		var snapshot struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal([]byte(summary), &snapshot)
		modelSource := ""
		if action == "automatic" {
			modelSource = "unknown"
			if fallback.Valid {
				snapshot.Model = attemptModel
				modelSource = "primary"
				if fallback.Bool {
					modelSource = "fallback"
				}
			}
		}
		out = append(out, map[string]any{"id": id, "category": cat, "action": action, "entry": entry, "operation": op, "run": run, "old_path": old, "new_path": new, "message": msg, "created": created, "model": snapshot.Model, "model_config_title": modelTitle, "model_source": modelSource, "tokens": tokens, "token_estimated": tokenEstimated > 0})
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
	rows.Close()
	if err := tx.Commit(); err != nil {
		failure(w, err)
		return
	}
	if q.Get("paged") == "1" || q.Has("page") {
		send(w, 200, map[string]any{"items": out, "total": total, "next": next, "page": page})
		return
	}
	send(w, 200, out)
}
func (a *App) items(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, run := number(q.Get("after")), number(r.PathValue("id"))
	tx, err := a.DB.Read().BeginTx(r.Context(), nil)
	if err != nil {
		failure(w, err)
		return
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(r.Context(), "SELECT count(*) FROM run_items WHERE run=?", run).Scan(&total); err != nil {
		failure(w, err)
		return
	}
	page, offset := int64(1), int64(0)
	if q.Has("page") {
		page = min(max(1, number(q.Get("page"))), max(1, (total+99)/100))
		offset = (page - 1) * 100
		after = 0
	}
	rows, err := tx.QueryContext(r.Context(), "SELECT id,entry,status,message,original_path FROM run_items WHERE run=? AND id>? ORDER BY id LIMIT 100 OFFSET ?", run, after, offset)
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
	if err := tx.Commit(); err != nil {
		failure(w, err)
		return
	}
	for _, item := range out {
		item["current_path"] = a.Library.DisplayPath(item["entry"].(int64))
		attempts, err := jobs.Attempts(a.DB, item["id"].(int64))
		if err != nil {
			failure(w, err)
			return
		}
		item["attempts"] = attempts
	}
	if q.Has("page") {
		send(w, 200, map[string]any{"items": out, "total": total, "page": page})
		return
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
