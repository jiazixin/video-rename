package jobs

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/models"
	"video-rename/internal/sampling"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

func (m *Manager) Execute(parent context.Context, id int64) {
	r, err := m.Get(parent, id)
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
		if err := m.failRun(id, target, e); err != nil {
			slog.Error("保存批次失败状态失败，保留活动状态等待重启恢复", "run", id, "error", err, "cause", e)
		}
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
	var runConfig config.TaskConfig
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
		runConfig.FailureKeywords = r.retry.FailureKeywords
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
		if err != nil && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
			fail(err)
			return
		}
	}
	if err = m.completeRun(id, ctx.Err() != nil); err != nil {
		fail(fmt.Errorf("保存批次结束状态失败: %w", err))
	}
}
