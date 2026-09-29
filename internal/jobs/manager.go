// Package jobs owns persistent batches and the bounded media worker pool.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/discovery"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/library"
	"video-rename/internal/models"
	"video-rename/internal/pipeline"
	"video-rename/internal/sampling"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

type Run struct {
	retry            retryPolicy
	Task             string         `json:"task"`
	TargetNames      []string       `json:"target_names,omitempty"`
	RequestConfig    jsontext.Value `json:"request_config,omitempty"`
	ModelConfigID    int64          `json:"model_config_id"`
	ModelConfigTitle string         `json:"model_config_title"`
	PromptBody       string         `json:"prompt_body"`
	ID               int64          `json:"id"`
	Source           string         `json:"source"`
	Targets          []int64        `json:"targets"`
	Recursive        bool           `json:"recursive"`
	Status           string         `json:"status"`
	Created          string         `json:"created"`
	Started          string         `json:"started"`
	Finished         string         `json:"finished"`
	Discovered       int            `json:"discovered"`
	Done             int            `json:"done"`
	Skipped          int            `json:"skipped"`
	Failed           int            `json:"failed"`
	TokenCount       int64          `json:"token_count"`
	TokenRecords     int            `json:"token_records"`
	TokenEstimated   int            `json:"token_estimated_records"`
	Error            string         `json:"error"`
	Plan             int64          `json:"plan"`
}
type Request struct {
	Task          string  `json:"task"`
	ModelConfigID int64   `json:"model_config_id"`
	Targets       []int64 `json:"targets"`
	Recursive     bool    `json:"recursive"`
	Key           string  `json:"key"`
}
type Manager struct {
	DB           *storage.Store
	Library      *library.Library
	Config       *config.Config
	createMu     sync.Mutex
	mu           sync.Mutex
	active       map[int64]activeRun
	Cache        *framecache.Cache
	extractSlots limiter
	modelSlots   limiter
	Prepare      func(context.Context, *config.Config, domain.VideoJob, string, string, string) (domain.FrameSet, error)
	Infer        func(context.Context, *config.Config, []domain.Frame) (string, error)
	Analyze      func(context.Context, *config.Config, domain.VideoJob, string, string) (string, error)
}

type activeRun struct {
	targets []int64
	cancel  context.CancelFunc
	control *runControl
}

func New(db *storage.Store, l *library.Library, cfg *config.Config) (*Manager, error) {
	if err := (models.Store{DB: db}).Seed(); err != nil {
		return nil, err
	}
	if err := interruptRuns(db); err != nil {
		return nil, err
	}
	if err := recoverExtractions(db); err != nil {
		return nil, err
	}
	settings, err := (systemsettings.Store{DB: db}).Get()
	if err != nil {
		return nil, err
	}
	temp := cfg.TempStorage
	if temp == "" {
		var seq int
		var name, path string
		if err = db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
			return nil, err
		}
		temp = filepath.Join(filepath.Dir(path), ".cache")
	}
	cache, err := framecache.New(temp, settings.Bytes(), settings.Policy)
	if err != nil {
		return nil, err
	}
	m := &Manager{DB: db, Library: l, Config: cfg, active: map[int64]activeRun{}, Cache: cache, Prepare: pipeline.Prepare, Infer: pipeline.Infer}
	l.Busy = m.Busy
	return m, nil
}

func interruptRuns(db *storage.Store) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err = tx.Exec(`UPDATE run_items SET status='cancelled',message='服务重启，处理已中断',finished_ms=?
WHERE status IN ('queued','running') AND run IN (SELECT id FROM runs WHERE status IN ('queued','scanning','running','paused','cancelling'))`, now.UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE runs SET status='interrupted',finished=?,error='服务重启，前次运行已中断' WHERE status IN ('queued','scanning','running','paused','cancelling')", now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	// Correct terminal batches previously classified as partial despite all
	// items failing. Keep counts, timestamps and diagnostics unchanged.
	if _, err = tx.Exec(`UPDATE runs SET status='failed'
WHERE status='partial_failure' AND failed>0 AND done=0 AND skipped=0`); err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) Busy(id int64) bool {
	m.mu.Lock()
	var targets []int64
	for r := range maps.Values(m.active) {
		targets = append(targets, r.targets...)
	}
	m.mu.Unlock()
	for _, t := range targets {
		if m.Library.IsWithin(id, t) || m.Library.IsWithin(t, id) {
			return true
		}
	}
	return false
}
func (m *Manager) Tools() (string, string, error) {
	p, e := exec.LookPath("ffprobe")
	if e != nil {
		return "", "", fmt.Errorf("未找到 ffprobe")
	}
	f, e := exec.LookPath("ffmpeg")
	if e != nil {
		return "", "", fmt.Errorf("未找到 ffmpeg")
	}
	return p, f, nil
}
func (m *Manager) Create(r Request, source string, plan int64) (int64, error) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if len(r.Targets) == 0 || len(r.Key) < 8 || len(r.Key) > 180 {
		return 0, fmt.Errorf("请选择目录或文件，并提供有效请求键")
	}
	if r.Task == "" {
		r.Task = "rename"
	}
	if r.Task != "rename" && r.Task != "extract" {
		return 0, fmt.Errorf("请选择重命名或抽帧任务")
	}
	if r.Task == "extract" {
		r.ModelConfigID = 0
	}
	// Normalize scopes without scanning them. Overlapping recursive targets are redundant.
	index := newScopeIndex(m.DB)
	normalized, err := index.normalize(r.Targets, r.Recursive)
	if err != nil {
		return 0, err
	}
	r.Targets = normalized
	if r.Task == "rename" && r.ModelConfigID == 0 {
		var err error
		r.ModelConfigID, err = (models.Store{DB: m.DB}).DefaultID()
		if err != nil {
			return 0, err
		}
	}
	var oldModelConfigID int64
	var old int64
	var oldTargets string
	var oldRecursive bool
	var oldTask string
	err = m.DB.QueryRow("SELECT id,targets,recursive,model_config_id,task FROM runs WHERE request_key=?", r.Key).Scan(&old, &oldTargets, &oldRecursive, &oldModelConfigID, &oldTask)
	if err == nil {
		current, _ := json.Marshal(r.Targets)
		if oldTargets != string(current) || oldRecursive != r.Recursive || oldModelConfigID != r.ModelConfigID || oldTask != r.Task {
			return 0, fmt.Errorf("请求键已被不同的运行范围、任务类型或模型配置使用")
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err = m.checkOverlap(r, index); err != nil {
		return 0, err
	}
	if _, _, err = m.Tools(); err != nil {
		return 0, err
	}
	for _, id := range r.Targets {
		e, err := index.get(id)
		if err != nil {
			return 0, err
		}
		if e.kind != "directory" && (e.kind != "file" || !discovery.IsVideoExtension(e.name)) {
			return 0, fmt.Errorf("只能运行目录或视频文件")
		}
	}
	frames, err := (sampling.Store{DB: m.DB}).Get()
	if err != nil {
		return 0, err
	}
	frameSnapshot, err := json.Marshal(frames)
	if err != nil {
		return 0, err
	}
	cfg := *m.Config
	cfg.Sampling, cfg.Images = frames.Sampling, frames.Images
	var prompt models.Profile
	var snapshot string
	if r.Task == "rename" {
		prompt, err = (models.Store{DB: m.DB}).Get(r.ModelConfigID)
		if err != nil {
			return 0, fmt.Errorf("请选择有效的模型配置: %w", err)
		}
		if err = prompt.Validate(); err != nil {
			return 0, err
		}
		fixed, err := (systemsettings.Store{DB: m.DB}).FixedPrompt()
		if err != nil {
			return 0, err
		}
		// The fixed prompt rides below the selected prompt as a hard constraint;
		// the snapshot stores the combined text so the run replays exactly what
		// was sent, regardless of later edits to the library or the fixed part.
		prompt.Body = strings.TrimRight(prompt.Body, "\n") + "\n\n" + strings.TrimRight(fixed, "\n")
		if err = cfg.ApplyModel(prompt.ServerConfig, prompt.Body); err != nil {
			return 0, fmt.Errorf("请完善模型配置: %w", err)
		}
		snapshot, err = (models.Store{DB: m.DB}).Snapshot(prompt)
		if err != nil {
			return 0, err
		}
	}
	retry, err := m.freezeRetry(r.Task, r.ModelConfigID)
	if err != nil {
		return 0, err
	}
	retryRaw, err := json.Marshal(retry)
	if err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(r.Targets)
	roles := []string{"system", "user"}
	systemLocation := "messages"
	if cfg.Server.Protocol == "anthropic" {
		roles = []string{"user"}
		systemLocation = "top_level"
	}
	tokenParameter := "max_tokens"
	if cfg.Server.Provider == "openai" {
		tokenParameter = "max_completion_tokens"
	}
	summary, _ := json.Marshal(map[string]any{"retry_enabled": retry.Enabled, "retry_count": retry.Count, "fallback_model_title": retry.FallbackTitle, "model": cfg.Server.Model, "provider": cfg.Server.Provider, "base_url": cfg.Server.BaseURL, "protocol": cfg.Server.Protocol, "omit_temperature": cfg.Server.OmitTemperature, "thinking": cfg.Server.Thinking, "thinking_adapter": cfg.Server.ThinkingAdapter, "thinking_effort": cfg.Server.ThinkingEffort, "thinking_budget": cfg.Server.ThinkingBudget, "prompt_hash": cfg.PromptHash(), "sampling": cfg.Sampling, "images": cfg.Images, "workers": cfg.Workers, "temperature": cfg.Server.Temperature, "max_tokens": cfg.Server.MaxTokens, "token_parameter": tokenParameter, "stream": false, "message_roles": roles, "system_location": systemLocation})
	if r.Task == "extract" {
		summary, _ = json.Marshal(map[string]any{"sampling": cfg.Sampling, "images": cfg.Images})
	}
	tx, err := m.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM runs WHERE status IN ('queued','scanning','running','paused','cancelling')").Scan(&count); err != nil {
		return 0, err
	}
	if count >= 20 {
		return 0, fmt.Errorf("任务队列已满，请稍后重试")
	}
	res, err := tx.Exec("INSERT INTO runs(source,targets,recursive,status,created,request_key,plan,config_summary,model_config_id,model_config_title,prompt_body,model_snapshot,sampling_snapshot,task,retry_snapshot) VALUES(?,?,?,'queued',?,?,?,?,?,?,?,?,?,?,?)", source, string(raw), r.Recursive, storage.Now(), r.Key, plan, string(summary), prompt.ID, prompt.Title, prompt.Body, snapshot, string(frameSnapshot), r.Task, string(retryRaw))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

const runColumns = "r.id,r.source,r.targets,r.recursive,r.status,r.created,r.finished,r.discovered,r.done,r.skipped,r.failed,r.error,r.plan,r.model_config_id,r.model_config_title,r.prompt_body,r.config_summary,r.started,COALESCE(u.token_count,0),COALESCE(u.token_records,0),COALESCE(u.token_estimated_records,0),r.task"

func scanRun(row interface{ Scan(...any) error }) (r Run, err error) {
	var raw, summary string
	err = row.Scan(&r.ID, &r.Source, &raw, &r.Recursive, &r.Status, &r.Created, &r.Finished, &r.Discovered, &r.Done, &r.Skipped, &r.Failed, &r.Error, &r.Plan, &r.ModelConfigID, &r.ModelConfigTitle, &r.PromptBody, &summary, &r.Started, &r.TokenCount, &r.TokenRecords, &r.TokenEstimated, &r.Task)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &r.Targets)
		if jsontext.Value(summary).IsValid() {
			r.RequestConfig = jsontext.Value(summary)
		}
	}
	return
}
func (m *Manager) Get(id int64) (Run, error) {
	r, err := scanRun(m.DB.QueryRow("SELECT "+runColumns+" FROM runs r LEFT JOIN run_token_usage u ON u.run=r.id WHERE r.id=?", id))
	if err == nil {
		for _, target := range r.Targets {
			r.TargetNames = append(r.TargetNames, m.Library.DisplayPath(target))
		}
	}
	return r, err
}
func (m *Manager) List(before int64) ([]Run, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := m.DB.Query("SELECT "+runColumns+" FROM runs r LEFT JOIN run_token_usage u ON u.run=r.id WHERE r.id<? ORDER BY r.id DESC LIMIT 50", before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (m *Manager) Cancel(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, running := m.active[id]
	status, finished := "cancelled", storage.Now()
	if running {
		status, finished = "cancelling", ""
	}
	_, err := m.DB.Exec("UPDATE runs SET status=?,finished=? WHERE id=? AND status IN ('queued','scanning','running','paused')", status, finished, id)
	if err == nil {
		if r, ok := m.active[id]; ok {
			r.cancel()
		}
	}
	return err
}
func (m *Manager) Loop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { m.Cache.Loop(ctx) })
	launched := map[int64]bool{}
	finished := make(chan int64, 20)
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-finished:
			delete(launched, id)
		case <-ticker.C:
			rows, err := m.DB.QueryContext(ctx, "SELECT id FROM runs WHERE status='queued' ORDER BY id LIMIT 20")
			if err != nil {
				continue
			}
			var ids []int64
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				if launched[id] || len(launched) >= 20 {
					continue
				}
				launched[id] = true
				wg.Go(func() { m.Execute(ctx, id); finished <- id })
			}
		}
	}
}
func (m *Manager) Execute(parent context.Context, id int64) {
	r, err := m.Get(id)
	if err != nil || r.Status != "queued" {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m.mu.Lock()
	if _, ok := m.active[id]; ok {
		m.mu.Unlock()
		return
	}
	claim, err := m.DB.Exec("UPDATE runs SET status='scanning',started=? WHERE id=? AND status='queued'", storage.Now(), id)
	if err != nil {
		m.mu.Unlock()
		return
	}
	claimed, _ := claim.RowsAffected()
	if claimed == 0 {
		m.mu.Unlock()
		return
	}
	scheduleCtx, stop := context.WithCancel(ctx)
	m.active[id] = activeRun{targets: r.Targets, cancel: cancel, control: &runControl{phase: "scanning", parent: ctx, schedule: scheduleCtx, stop: stop, changed: make(chan struct{})}}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.active, id); m.mu.Unlock() }()

	failAt := func(e error, target int64) {
		_, _ = m.DB.Exec("UPDATE runs SET status='failed',failed=failed+CASE WHEN ?>0 THEN 1 ELSE 0 END,error=?,finished=? WHERE id=?", target, e.Error(), storage.Now(), id)
		path, action := "", "run"
		if target > 0 {
			path, action = m.Library.DisplayPath(target), "scan"
		}
		if r.Task == "extract" {
			if target == 0 {
				path = strings.Join(r.TargetNames, "、")
			}
			m.extractionFailure(r, target, path, e)
		} else {
			if logErr := m.recordFailure(action, target, id, path, e); logErr != nil {
				slog.Error("保存批次失败诊断失败", "run", id, "error", logErr)
			}
		}
	}
	fail := func(e error) { failAt(e, 0) }
	if r.Task != "extract" && r.PromptBody == "" {
		fail(fmt.Errorf("任务没有模型配置快照，请重新创建任务"))
		return
	}
	runConfig := *m.Config
	var frameSnapshot string
	if err = m.DB.QueryRow("SELECT sampling_snapshot FROM runs WHERE id=?", id).Scan(&frameSnapshot); err != nil {
		fail(err)
		return
	}
	frames, frameErr := sampling.Parse(frameSnapshot)
	if frameErr != nil {
		fail(fmt.Errorf("抽帧快照无效: %w", frameErr))
		return
	}
	runConfig.Sampling, runConfig.Images = frames.Sampling, frames.Images
	if r.Task != "extract" {
		var snapshot, retryRaw string
		if err = m.DB.QueryRow("SELECT model_snapshot,retry_snapshot FROM runs WHERE id=?", id).Scan(&snapshot, &retryRaw); err != nil {
			fail(err)
			return
		}
		if retryRaw != "" {
			if err = json.Unmarshal([]byte(retryRaw), &r.retry); err != nil {
				fail(err)
				return
			}
		}
		if err = (models.Store{DB: m.DB}).ApplySnapshot(snapshot, &runConfig); err != nil {
			fail(err)
			return
		}
	}
	settings, err := (systemsettings.Store{DB: m.DB}).Get()
	if err != nil {
		fail(err)
		return
	}
	stableDelay := time.Duration(settings.StabilitySeconds) * time.Second
	// Media mounts may be slow. Validate every selected path in the worker,
	// before creating any items, so submission never waits for media I/O.
	for _, target := range r.Targets {
		if _, err = m.dispatchContext(ctx, id); err != nil {
			break
		}
		if _, err = m.Library.Path(target); err != nil {
			failAt(err, target)
			return
		}
	}
	for _, target := range r.Targets {
		if err = m.scan(ctx, r, target); err != nil {
			if ctx.Err() != nil {
				break
			}
			fail(err)
			return
		}
	}
	var pending int
	if e := m.DB.QueryRow("SELECT count(*) FROM run_items WHERE run=? AND status='queued'", id).Scan(&pending); e != nil {
		fail(e)
		return
	}
	if ctx.Err() == nil && stableDelay > 0 && pending > 0 {
		timer := time.NewTimer(stableDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if ctx.Err() == nil {
		if err = m.setPhase(id, "running"); err != nil {
			fail(err)
			return
		}
		err = m.processPending(ctx, r, runConfig)
		if err != nil && ctx.Err() == nil {
			fail(err)
			return
		}
	}
	status := "completed"
	if ctx.Err() != nil {
		status = "cancelled"
		current, _ := m.Get(id)
		if current.Error != "" {
			status = "failed"
		}
		_, _ = m.DB.Exec("UPDATE run_items SET status='cancelled',finished_ms=? WHERE run=? AND status IN ('queued','running')", time.Now().UnixMilli(), id)
	} else {
		r, _ = m.Get(id)
		if r.Failed > 0 {
			status = "failed"
			if r.Done > 0 || r.Skipped > 0 {
				status = "partial_failure"
			}
		}
	}
	_, _ = m.DB.Exec("UPDATE runs SET status=?,finished=? WHERE id=?", status, storage.Now(), id)
}

type item struct {
	ID, Entry int64
	Identity  string
	Size      int64
	Mtime     string
}

func (m *Manager) scan(ctx context.Context, batch Run, id int64) error {
	run := batch.ID
	if _, err := m.dispatchContext(ctx, run); err != nil {
		return err
	}
	e, err := m.Library.IndexedEntry(id)
	if err != nil {
		return err
	}
	if e.Kind == "file" {
		return m.queueFiles(ctx, batch, []library.Entry{e})
	}
	if e.Kind != "directory" {
		return nil
	}
	if err = m.Library.Refresh(ctx, id); err != nil {
		if batch.Task == "extract" {
			m.extractionFailure(batch, id, m.Library.DisplayPath(id), err)
		} else if er := m.recordFailure("scan", id, run, m.Library.DisplayPath(id), err); er != nil {
			return er
		}
		_, er := m.DB.Exec("UPDATE runs SET failed=failed+1 WHERE id=?", run)
		return er
	}
	var after int64
	for {
		if _, err := m.dispatchContext(ctx, run); err != nil {
			return err
		}
		rows, err := m.DB.Read().QueryContext(ctx, `SELECT e.id,e.parent,e.name,e.kind,e.identity,e.size,e.mtime,
        COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),'')
        FROM entries e WHERE e.parent=? AND e.present=1 AND e.id>? ORDER BY e.id LIMIT 128`, id, after)
		if err != nil {
			return err
		}
		children := []library.Entry{}
		for rows.Next() {
			var c library.Entry
			if err = rows.Scan(&c.ID, &c.Parent, &c.Name, &c.Kind, &c.Identity, &c.Size, &c.Mtime, &c.Processed); err != nil {
				break
			}
			children = append(children, c)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if len(children) == 0 {
			return nil
		}
		if err = m.queueFiles(ctx, batch, children); err != nil {
			return err
		}
		for _, c := range children {
			after = c.ID
			if batch.Recursive && c.Kind == "directory" {
				if err = m.scan(ctx, batch, c.ID); err != nil {
					return err
				}
			}
		}
	}
}
func (m *Manager) process(ctx context.Context, batch Run, it item, cfg *config.Config, extracted func()) {
	run := batch.ID
	if ctx.Err() != nil {
		return
	}
	committed := false
	extractionLogged := false
	finish := func(status, msg string) {
		_, _ = m.DB.Exec("UPDATE run_items SET status=?,message=?,finished_ms=? WHERE id=?", status, msg, time.Now().UnixMilli(), it.ID)
		column := "done"
		if status == "skipped" {
			column = "skipped"
		}
		if status == "failed" {
			column = "failed"
		}
		if status != "cancelled" {
			_, _ = m.DB.Exec("UPDATE runs SET "+column+"="+column+"+1 WHERE id=?", run)
		}
	}
	e, err := m.Library.Get(it.Entry)
	if err == nil && e.Processed != "" && batch.Task != "extract" {
		finish("skipped", "已处理，跳过")
		return
	}
	var p string
	if err == nil {
		p, err = m.Library.Path(it.Entry)
	}
	var snap domain.Snapshot
	if err == nil {
		snap, err = domain.StatSnapshot(p)
	}
	if err == nil && (library.Identity(snap) != it.Identity || snap.Size != it.Size || snap.ModTime.UTC().Format(time.RFC3339Nano) != it.Mtime) {
		if batch.Task == "extract" {
			m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), fmt.Errorf("文件尚不稳定，留待下次运行"))
		}
		finish("skipped", "文件尚不稳定，留待下次运行")
		return
	}
	if err == nil {
		_, err = m.DB.Exec("UPDATE run_items SET status='running',started_ms=? WHERE id=?", time.Now().UnixMilli(), it.ID)
	}
	if err == nil {
		err = m.Library.CheckAvailable(it.Entry)
	}
	if err == nil {
		probe, ffmpeg, te := m.Tools()
		err = te
		if te != nil {
			m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), te)
			extractionLogged = true
		}
		if err == nil {
			if batch.Task == "extract" {
				var lease *framecache.Lease
				lease, err = m.prepareFrames(ctx, batch, it.Entry, cfg, domain.VideoJob{Path: p, Snapshot: snap, JobID: fmt.Sprint(it.ID)}, probe, ffmpeg, extracted)
				extractionLogged = true
				extracted()
				if lease != nil {
					lease.Release(false)
				}
			} else {
				var title string
				var exchange domain.ModelDiagnostics
				var lease *framecache.Lease
				title, lease, err = m.analyze(domain.WithModelDiagnostics(ctx, &exchange), batch, it.Entry, cfg, domain.VideoJob{Path: p, Snapshot: snap, JobID: fmt.Sprint(it.ID)}, probe, ffmpeg, extracted)
				if lease != nil {
					defer func() { lease.Release(committed) }()
				}
				if err == nil && ctx.Err() == nil {
					_, err = m.Library.AutoCommit(e.ID, snap, title, fmt.Sprintf("run:%d:item:%d", run, it.ID), run, exchange)
					committed = err == nil
				}
			}
		}
	}
	if ctx.Err() != nil && !committed {
		finish("cancelled", "已取消")
		return
	}
	if err != nil {
		if batch.Task == "extract" {
			if !extractionLogged {
				m.extractionFailure(batch, it.Entry, m.Library.DisplayPath(it.Entry), err)
			}
		} else {
			rel, _ := filepath.Rel(m.Library.Root, p)
			if logErr := m.recordFailure("automatic", it.Entry, run, rel, err); logErr != nil {
				slog.Error("保存视频失败诊断失败", "run", run, "entry", it.Entry, "error", logErr)
			}
		}
		finish("failed", err.Error())
		if je, ok := errors.AsType[*domain.JobError](err); (ok && je.Global) || m.Library.Halted() {
			m.mu.Lock()
			if r, ok := m.active[run]; ok {
				r.cancel()
			}
			m.mu.Unlock()
			_, _ = m.DB.Exec("UPDATE runs SET error=? WHERE id=?", err.Error(), run)
		}
		return
	}
	finish("done", "")
}
