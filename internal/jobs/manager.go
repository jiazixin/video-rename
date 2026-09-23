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
	"os/exec"
	"path/filepath"
	"slices"
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
	"video-rename/internal/tokenusage"
)

type Run struct {
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
}

func New(db *storage.Store, l *library.Library, cfg *config.Config) (*Manager, error) {
	if err := (models.Store{DB: db}).Seed(); err != nil {
		return nil, err
	}
	if _, err := db.Exec("UPDATE runs SET status='interrupted',finished=?,error='服务重启，前次运行已中断' WHERE status IN ('queued','scanning','running','cancelling')", storage.Now()); err != nil {
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
	cache, err := framecache.New(temp, settings.Bytes())
	if err != nil {
		return nil, err
	}
	m := &Manager{DB: db, Library: l, Config: cfg, active: map[int64]activeRun{}, Cache: cache, Prepare: pipeline.Prepare, Infer: pipeline.Infer}
	l.Busy = m.Busy
	return m, nil
}
func (m *Manager) Busy(id int64) bool {
	m.mu.Lock()
	var targets []int64
	for _, r := range m.active {
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
	if len(r.Targets) == 0 || len(r.Targets) > 100 || len(r.Key) < 8 || len(r.Key) > 180 {
		return 0, fmt.Errorf("请选择 1–100 个目录或文件，并提供请求键")
	}
	// Normalize scopes without scanning them. Overlapping recursive targets are redundant.
	slices.Sort(r.Targets)
	normalized := make([]int64, 0, len(r.Targets))
	for _, id := range r.Targets {
		covered := false
		for _, other := range r.Targets {
			if other != id && r.Recursive && m.Library.IsWithin(id, other) {
				covered = true
				break
			}
		}
		if !covered && (len(normalized) == 0 || normalized[len(normalized)-1] != id) {
			normalized = append(normalized, id)
		}
	}
	r.Targets = normalized
	if r.ModelConfigID == 0 {
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
	err := m.DB.QueryRow("SELECT id,targets,recursive,model_config_id FROM runs WHERE request_key=?", r.Key).Scan(&old, &oldTargets, &oldRecursive, &oldModelConfigID)
	if err == nil {
		current, _ := json.Marshal(r.Targets)
		if oldTargets != string(current) || oldRecursive != r.Recursive || oldModelConfigID != r.ModelConfigID {
			return 0, fmt.Errorf("请求键已被不同的运行范围或模型配置使用")
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err = m.checkOverlap(r); err != nil {
		return 0, err
	}
	if _, _, err = m.Tools(); err != nil {
		return 0, err
	}
	for _, id := range r.Targets {
		e, err := m.Library.Get(id)
		if err != nil {
			return 0, err
		}
		if e.Kind != "directory" && (e.Kind != "file" || !discovery.IsVideoExtension(e.Name)) {
			return 0, fmt.Errorf("只能运行目录或视频文件")
		}
		if _, err = m.Library.Path(id); err != nil {
			return 0, err
		}
	}
	prompt, err := (models.Store{DB: m.DB}).Get(r.ModelConfigID)
	if err != nil {
		return 0, fmt.Errorf("请选择有效的模型配置: %w", err)
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
	if err = cfg.ApplyModel(prompt.ServerConfig, prompt.Body); err != nil {
		return 0, fmt.Errorf("请完善模型配置: %w", err)
	}
	if err = prompt.Validate(); err != nil {
		return 0, err
	}
	snapshot, err := (models.Store{DB: m.DB}).Snapshot(prompt)
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
	summary, _ := json.Marshal(map[string]any{"model": cfg.Server.Model, "provider": cfg.Server.Provider, "base_url": cfg.Server.BaseURL, "protocol": cfg.Server.Protocol, "omit_temperature": cfg.Server.OmitTemperature, "thinking": cfg.Server.Thinking, "thinking_adapter": cfg.Server.ThinkingAdapter, "thinking_effort": cfg.Server.ThinkingEffort, "thinking_budget": cfg.Server.ThinkingBudget, "prompt_hash": cfg.PromptHash(), "sampling": cfg.Sampling, "images": cfg.Images, "workers": cfg.Workers, "temperature": cfg.Server.Temperature, "max_tokens": cfg.Server.MaxTokens, "token_parameter": tokenParameter, "stream": false, "message_roles": roles, "system_location": systemLocation})
	tx, err := m.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM runs WHERE status IN ('queued','scanning','running','cancelling')").Scan(&count); err != nil {
		return 0, err
	}
	if count >= 20 {
		return 0, fmt.Errorf("任务队列已满，请稍后重试")
	}
	res, err := tx.Exec("INSERT INTO runs(source,targets,recursive,status,created,request_key,plan,config_summary,model_config_id,model_config_title,prompt_body,model_snapshot,sampling_snapshot) VALUES(?,?,?,'queued',?,?,?,?,?,?,?,?,?)", source, string(raw), r.Recursive, storage.Now(), r.Key, plan, string(summary), prompt.ID, prompt.Title, prompt.Body, snapshot, string(frameSnapshot))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

const runColumns = "r.id,r.source,r.targets,r.recursive,r.status,r.created,r.finished,r.discovered,r.done,r.skipped,r.failed,r.error,r.plan,r.model_config_id,r.model_config_title,r.prompt_body,r.config_summary,r.started,COALESCE(u.token_count,0),COALESCE(u.token_records,0),COALESCE(u.token_estimated_records,0)"

func scanRun(row interface{ Scan(...any) error }) (r Run, err error) {
	var raw, summary string
	err = row.Scan(&r.ID, &r.Source, &raw, &r.Recursive, &r.Status, &r.Created, &r.Finished, &r.Discovered, &r.Done, &r.Skipped, &r.Failed, &r.Error, &r.Plan, &r.ModelConfigID, &r.ModelConfigTitle, &r.PromptBody, &summary, &r.Started, &r.TokenCount, &r.TokenRecords, &r.TokenEstimated)
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
	if r, ok := m.active[id]; ok {
		r.cancel()
	}
	_, err := m.DB.Exec("UPDATE runs SET status=CASE WHEN status='queued' THEN 'cancelled' ELSE 'cancelling' END,finished=CASE WHEN status='queued' THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') ELSE finished END WHERE id=? AND status IN ('queued','scanning','running')", id)
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
	m.active[id] = activeRun{targets: r.Targets, cancel: cancel}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.active, id); m.mu.Unlock() }()
	claim, err := m.DB.Exec("UPDATE runs SET status='scanning',started=? WHERE id=? AND status='queued'", storage.Now(), id)
	if err != nil {
		return
	}
	claimed, _ := claim.RowsAffected()
	if claimed == 0 {
		return
	}
	fail := func(e error) {
		_, _ = m.DB.Exec("UPDATE runs SET status='failed',error=?,finished=? WHERE id=?", e.Error(), storage.Now(), id)
		_ = m.Library.Event("failure", "run", 0, 0, id, "", "", e.Error())
	}
	if r.PromptBody == "" {
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
	var snapshot string
	if err = m.DB.QueryRow("SELECT model_snapshot FROM runs WHERE id=?", id).Scan(&snapshot); err != nil {
		fail(err)
		return
	}
	if err = (models.Store{DB: m.DB}).ApplySnapshot(snapshot, &runConfig); err != nil {
		fail(err)
		return
	}
	settings, err := (systemsettings.Store{DB: m.DB}).Get()
	if err != nil {
		fail(err)
		return
	}
	stableDelay := time.Duration(settings.StabilitySeconds) * time.Second
	for _, target := range r.Targets {
		if err = m.scan(ctx, id, target, r.Recursive); err != nil {
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
		if _, err = m.DB.Exec("UPDATE runs SET status='running' WHERE id=?", id); err != nil {
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
		_, _ = m.DB.Exec("UPDATE run_items SET status='cancelled' WHERE run=? AND status IN ('queued','running')", id)
	} else {
		r, _ = m.Get(id)
		if r.Failed > 0 {
			status = "partial_failure"
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

func (m *Manager) scan(ctx context.Context, run, id int64, recursive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e, err := m.Library.Get(id)
	if err != nil {
		return err
	}
	if e.Kind == "file" {
		if !discovery.IsVideoExtension(e.Name) {
			return nil
		}
		res, err := m.DB.Exec("INSERT OR IGNORE INTO run_items(run,entry,identity,size,mtime,original_path) VALUES(?,?,?,?,?,?)", run, e.ID, e.Identity, e.Size, e.Mtime, m.Library.DisplayPath(e.ID))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			_, err = m.DB.Exec("UPDATE runs SET discovered=discovered+1 WHERE id=?", run)
			if err == nil && e.Processed != "" {
				_, err = m.DB.Exec("UPDATE run_items SET status='skipped',message='已处理，跳过' WHERE run=? AND identity=?", run, e.Identity)
				if err == nil {
					_, err = m.DB.Exec("UPDATE runs SET skipped=skipped+1 WHERE id=?", run)
				}
			}
		}
		return err
	}
	if e.Kind != "directory" {
		return nil
	}
	if err = m.Library.Refresh(ctx, id); err != nil {
		if er := m.Library.Event("failure", "scan", id, 0, run, m.Library.Relative(id), "", err.Error()); er != nil {
			return er
		}
		_, er := m.DB.Exec("UPDATE runs SET failed=failed+1 WHERE id=?", run)
		return er
	}
	var after int64
	for {
		rows, err := m.DB.Query("SELECT id,kind FROM entries WHERE parent=? AND present=1 AND id>? ORDER BY id LIMIT 128", id, after)
		if err != nil {
			return err
		}
		type child struct {
			id   int64
			kind string
		}
		batch := []child{}
		for rows.Next() {
			var c child
			if err = rows.Scan(&c.id, &c.kind); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, c := range batch {
			after = c.id
			if c.kind == "file" || recursive && c.kind == "directory" {
				if err = m.scan(ctx, run, c.id, recursive); err != nil {
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
	finish := func(status, msg string) {
		_, _ = m.DB.Exec("UPDATE run_items SET status=?,message=? WHERE id=?", status, msg, it.ID)
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
	if err == nil && e.Processed != "" {
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
		finish("skipped", "文件尚不稳定，留待下次运行")
		return
	}
	if err == nil {
		_, err = m.DB.Exec("UPDATE run_items SET status='running' WHERE id=?", it.ID)
	}
	if err == nil {
		err = m.Library.CheckAvailable(it.Entry)
	}
	if err == nil {
		probe, ffmpeg, te := m.Tools()
		err = te
		if err == nil {
			var title string
			var exchange domain.ModelDiagnostics
			var lease *framecache.Lease
			title, lease, err = m.analyze(domain.WithModelDiagnostics(ctx, &exchange), batch, cfg, domain.VideoJob{Path: p, Snapshot: snap, JobID: fmt.Sprint(it.ID)}, probe, ffmpeg, extracted)
			request, response := exchange.Request, exchange.Response
			if err != nil {
				var diagnostic *domain.JobError
				if errors.As(err, &diagnostic) {
					request, response = diagnostic.RawRequest, diagnostic.RawResponse
				}
			}
			if err := tokenusage.Record(m.DB, run, it.ID, request, response, time.Now()); err != nil {
				slog.Error("记录 token 用量失败", "run", run, "item", it.ID, "error", err)
			}
			if lease != nil {
				defer func() { lease.Release(committed) }()
			}
			if err == nil && ctx.Err() == nil {
				_, err = m.Library.AutoCommit(e.ID, snap, title, fmt.Sprintf("run:%d:item:%d", run, it.ID), run, exchange)
				committed = err == nil
			}
		}
	}
	if ctx.Err() != nil && !committed {
		finish("cancelled", "已取消")
		return
	}
	if err != nil {
		rel, _ := filepath.Rel(m.Library.Root, p)
		var diagnostic *domain.JobError
		if errors.As(err, &diagnostic) && (diagnostic.RawRequest != "" || diagnostic.RawResponse != "" || diagnostic.ProcessingLog != "" || diagnostic.StderrTail != "") {
			_, _ = m.DB.Exec("INSERT INTO events(category,action,entry,run,old_path,message,created,raw_request,raw_response,processing_log) VALUES('failure','automatic',?,?,?,?,?,?,?,?)", it.Entry, run, rel, err.Error(), storage.Now(), diagnostic.RawRequest, diagnostic.RawResponse, diagnostic.ProcessingLog+diagnostic.StderrTail)
		} else {
			_ = m.Library.Event("failure", "automatic", it.Entry, 0, run, rel, "", err.Error())
		}
		finish("failed", err.Error())
		var je *domain.JobError
		if (errors.As(err, &je) && je.Global) || m.Library.Halted() {
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
