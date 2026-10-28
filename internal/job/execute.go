package job

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/model"
	"video-rename/internal/sampling"
	"video-rename/internal/storage"
	"video-rename/internal/systemsetting"
)

// Execute runs queued batch id until it finishes or parent ends.
func (m *Manager) Execute(parent context.Context, id int64) {
	r, err := m.Get(parent, id)
	if err != nil {
		m.logger.Error("run_read_failed", "run_id", id, "error", err)
		return
	}
	if r.Status != "queued" {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m.mu.Lock()
	if _, ok := m.active[id]; ok {
		m.mu.Unlock()
		return
	}
	claim, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE runs SET status='scanning',started=? WHERE id=? AND status='queued'", storage.Now(), id)
	})
	if err != nil {
		m.mu.Unlock()
		m.logger.Error("run_claim_failed", "run_id", id, "error", err)
		return
	}
	claimed, err := claim.RowsAffected()
	if err != nil {
		m.mu.Unlock()
		m.logger.Error("run_claim_failed", "run_id", id, "error", err)
		return
	}
	if claimed == 0 {
		m.mu.Unlock()
		return
	}
	m.active[id] = activeRun{targets: r.Targets, cancel: cancel, control: newRunControl()}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.active, id); m.mu.Unlock() }()

	failAt := func(e error, target int64) {
		if ctx.Err() != nil && errors.Is(e, ctx.Err()) {
			// A cancelled read is the cancellation itself, not a run failure.
			if err := m.completeRun(ctx, id, true); err != nil {
				m.logger.Error("run_failure_save_failed", "run_id", id, "error", err, "cause", e)
			}
			return
		}
		if err := m.failRun(ctx, id, target, e); err != nil {
			m.logger.Error("run_failure_save_failed", "run_id", id, "error", err, "cause", e)
		}
		path, action := "", "run"
		if target > 0 {
			path, action = m.diagnosticPath(ctx, target), "scan"
		}
		if r.Task == "extract" {
			if target == 0 {
				path = strings.Join(r.TargetNames, ", ")
			}
			m.extractionFailure(ctx, extractionFailureOptions{Run: r, Entry: target, Path: path, Cause: e})
		} else {
			if logErr := m.recordFailure(ctx, recordFailureOptions{Action: action, Entry: target, Run: id, Path: path, Cause: e}); logErr != nil {
				m.logger.Error("run_diagnostics_save_failed", "run_id", id, "error", logErr)
			}
		}
	}
	fail := func(e error) { failAt(e, 0) }
	if r.Task != "extract" && r.PromptBody == "" {
		fail(domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonModelSnapshotMissing, Message: "run has no model configuration snapshot; create the run again"}))
		return
	}
	var runConfig config.TaskConfig
	var frameSnapshot string
	if err = m.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT sampling_snapshot FROM runs WHERE id=?", id).Scan(&frameSnapshot)
	}); err != nil {
		fail(err)
		return
	}
	frames, err := sampling.Parse(frameSnapshot)
	if err != nil {
		fail(domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonSamplingSnapshotInvalid, Message: "sampling snapshot is invalid"}).Wrap(err))
		return
	}
	runConfig.Sampling, runConfig.Images = frames.Sampling, frames.Images
	if r.Task != "extract" {
		var snapshot, retryRaw string
		if err = m.db.View(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT model_snapshot,retry_snapshot FROM runs WHERE id=?", id).Scan(&snapshot, &retryRaw)
		}); err != nil {
			fail(err)
			return
		}
		if retryRaw != "" {
			if err = json.Unmarshal([]byte(retryRaw), &r.retry); err != nil {
				fail(err)
				return
			}
		}
		if err = (model.New(m.db)).ApplySnapshot(snapshot, &runConfig); err != nil {
			fail(err)
			return
		}
		runConfig.FailureKeywords = r.retry.FailureKeywords
	}
	settings, err := (systemsetting.New(m.db)).Get(ctx)
	if err != nil {
		fail(err)
		return
	}
	stableDelay := time.Duration(settings.StabilitySeconds) * time.Second
	// Media mounts may be slow. Validate every selected path in the worker,
	// before creating any items, so submission never waits for media I/O.
	for _, target := range r.Targets {
		if err = m.waitDispatch(ctx, id); err != nil {
			break
		}
		if _, err = m.Library.Path(ctx, target); err != nil {
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
	if err := m.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM run_items WHERE run=? AND status='queued'", id).Scan(&pending)
	}); err != nil {
		fail(err)
		return
	}
	if ctx.Err() == nil && stableDelay > 0 && pending > 0 {
		select {
		case <-ctx.Done():
		case <-m.after(stableDelay):
		}
	}
	if ctx.Err() == nil {
		if err = m.setPhase(ctx, id, "running"); err != nil {
			fail(err)
			return
		}
		err = m.processPending(ctx, r, runConfig)
		if err != nil && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
			fail(err)
			return
		}
	}
	if err = m.completeRun(ctx, id, ctx.Err() != nil); err != nil {
		fail(fmt.Errorf("save run result: %w", err))
	}
}
