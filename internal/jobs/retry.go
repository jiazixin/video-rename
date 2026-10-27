package jobs

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/framecache"
	"video-rename/internal/models"
	"video-rename/internal/naming"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
	"video-rename/internal/tokenusage"
)

type retryPolicy struct {
	RetryOperations  map[int64]int64
	Enabled          bool
	Count            int
	FallbackID       int64
	FallbackTitle    string
	FallbackSnapshot string
	FailureKeywords  []string
}

func (m *Manager) freezeRetry(ctx context.Context, task string, primary int64) (retryPolicy, error) {
	if task == "extract" {
		return retryPolicy{}, nil
	}
	settings, err := (systemsettings.Store{DB: m.DB}).Get(ctx)
	if err != nil {
		return retryPolicy{}, err
	}
	keywords, err := (systemsettings.Store{DB: m.DB}).FailureKeywords(ctx)
	if err != nil {
		return retryPolicy{}, err
	}
	p := retryPolicy{Enabled: settings.RetryEnabled, Count: settings.RetryCount, FailureKeywords: keywords}
	if !p.Enabled || settings.FallbackModelID == 0 || settings.FallbackModelID == primary {
		return p, nil
	}
	model, err := (models.Store{DB: m.DB}).Get(ctx, settings.FallbackModelID)
	if err != nil {
		return p, domain.Fail(domain.KindConflict, "fallback_model_unavailable", "fallback model is unavailable").Wrap(err)
	}
	if err = model.Validate(); err != nil {
		return p, domain.Fail(domain.KindInvalid, "fallback_model_invalid", "fallback model configuration is invalid").Wrap(err)
	}
	fixed, err := (systemsettings.Store{DB: m.DB}).FixedPrompt(ctx)
	if err != nil {
		return p, err
	}
	model.Body = strings.TrimRight(model.Body, "\n") + "\n\n" + strings.TrimRight(fixed, "\n")
	p.FallbackID, p.FallbackTitle = model.ID, model.Title
	p.FallbackSnapshot, err = (models.Store{DB: m.DB}).Snapshot(model)
	return p, err
}

// retryKind separates permanent model configuration failures from transient or
// output failures. File and extraction failures never enter the model retry loop.
func retryKind(err error) (retry, fallback bool, delay time.Duration) {
	e, ok := errors.AsType[*domain.JobError](err)
	if !ok {
		return false, false, 0
	}
	if e.Code == domain.CodeCancelled || e.Code == domain.CodeSourceChanged {
		return false, false, 0
	}
	if e.Code == domain.CodeAPIUnauthorized || e.Code == domain.CodeRequestTooLarge || (e.HTTPStatus >= 400 && e.HTTPStatus < 500 && e.HTTPStatus != 408 && e.HTTPStatus != 429) {
		return false, true, 0
	}
	switch e.Code {
	case domain.CodeAPITimeout, domain.CodeAPIRateLimited, domain.CodeAPIHTTPError, domain.CodeAPIResponseInvalid, domain.CodeAPIResponseTooLarge, domain.CodeTitleInvalid, domain.CodeNamingFailed, domain.CodeModelOutputTruncated:
		return true, true, e.RetryAfter
	}
	return false, false, 0
}
func retryWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// inferWithRetry calls the model for one item, retrying within the run's
// frozen policy. The final slot goes to the fallback model when one is
// configured, and an error the primary model cannot recover from skips
// straight to it. leaseRef is shared with the caller because waiting for a
// model slot or a retry may release the screenshot lease and acquire a new one.
func (m *Manager) inferWithRetry(ctx context.Context, run Run, entry int64, cfg *config.TaskConfig, job domain.VideoJob, probe, ffmpeg string, leaseRef **framecache.Lease) (string, error) {
	lease := *leaseRef
	defer func() { *leaseRef = lease }()
	var itemID int64
	if err := m.DB.QueryRowContext(ctx, "SELECT id FROM run_items WHERE run=? AND entry=?", run.ID, entry).Scan(&itemID); err != nil {
		return "", err
	}
	lastSlot := 0
	if run.retry.Enabled {
		lastSlot = run.retry.Count
	}
	hasFallback := run.retry.FallbackID > 0
	attempt := 0 // model calls made so far; slots that fail before calling do not count
	for slot := 0; slot <= lastSlot; slot++ {
		current := *cfg
		modelID, modelTitle := run.ModelConfigID, run.ModelConfigTitle
		fallback := hasFallback && slot > 0 && slot == lastSlot
		if fallback {
			if err := (models.Store{DB: m.DB}).ApplySnapshot(run.retry.FallbackSnapshot, &current); err != nil {
				return "", err
			}
			modelID, modelTitle = run.retry.FallbackID, run.retry.FallbackTitle
		}
		m.updateStage(itemID, func(s *LiveStage) {
			s.Stage, s.Attempt, s.Fallback = "model_wait", attempt+1, fallback
			if lease != nil {
				s.Frames = len(lease.Set.Frames)
			}
		})
		waitStarted := time.Now()
		release, workers, err := m.inferenceSlot(ctx, run, entry, modelID, &current, job, probe, ffmpeg, &lease)
		if err != nil {
			return "", err
		}
		current.Workers, current.Server.Workers = workers, workers
		if err = sourceUnchangedAfterWait(ctx, run, entry, job, lease, waitStarted); err != nil {
			release()
			return "", err
		}
		attempt++
		m.updateStage(itemID, func(s *LiveStage) {
			s.Stage = "model"
			if lease != nil {
				s.Frames = len(lease.Set.Frames)
			}
		})
		started := time.Now()
		var exchange domain.ModelDiagnostics
		requestCtx := domain.WithModelDiagnostics(ctx, &exchange)
		var result string
		if m.Analyze != nil {
			result, err = m.Analyze(requestCtx, &current, job, probe, ffmpeg)
		} else {
			result, err = m.Infer(requestCtx, &current, lease.Set.Frames)
		}
		release()
		if err == nil {
			err = m.checkTitle(itemID, result, &current, job, exchange)
		}
		if diagnostic, ok := errors.AsType[*domain.JobError](err); ok {
			exchange.Request, exchange.Response = diagnostic.RawRequest, diagnostic.RawResponse
		}
		attemptRecord := modelAttemptRecord{item: itemID, number: attempt, modelID: modelID, modelTitle: modelTitle, fallback: fallback, started: started, err: err}
		if recordErr := m.recordModelAttempt(ctx, run, attemptRecord, &current, exchange); recordErr != nil {
			return "", recordErr
		}
		if err == nil {
			domain.CaptureModelDiagnostics(ctx, domain.TitleResult{RawRequest: exchange.Request, RawResponse: exchange.Response})
			return result, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		again, canFallback, retryAfter := retryKind(err)
		if fallback || slot == lastSlot {
			return "", err
		}
		if !again {
			if !canFallback || !hasFallback {
				return "", err
			}
			slot = lastSlot - 1 // the loop increment moves to the fallback slot
			continue
		}
		m.setStage(itemID, "retry_wait")
		if err = m.waitBeforeRetry(ctx, run, retryBackoff(slot, retryAfter), &lease); err != nil {
			return "", err
		}
	}
	return "", errors.New("model request did not complete")
}

// sourceUnchangedAfterWait rejects a video that changed while the item waited
// for a model slot, before any model is called.
func sourceUnchangedAfterWait(ctx context.Context, run Run, entry int64, job domain.VideoJob, lease *framecache.Lease, waitStarted time.Time) error {
	snap, err := domain.StatSnapshot(job.Path)
	if err != nil {
		return err
	}
	if !snap.Same(job.Snapshot) {
		diagnostic := domain.SourceChangedError("video changed while waiting for the model", domain.StageBeforeRequest, job.Path, job.Snapshot, snap)
		diagnostic.ProcessingLog += fmt.Sprintf("run: %d\nentry: %d\nwaited for model: %s\nbefore: source snapshot when processing started; after: source snapshot once a model slot was acquired.\nThe model was not called; the check covers the source video, not the frames.\n", run.ID, entry, time.Since(waitStarted))
		if lease != nil {
			diagnostic.ProcessingLog += fmt.Sprintf("frames prepared: %d\n", len(lease.Set.Frames))
		}
		return diagnostic
	}
	return ctx.Err()
}

// checkTitle applies the run's failure rules and the naming rules to a model
// reply, showing the accepted name as the item's live title.
func (m *Manager) checkTitle(itemID int64, result string, current *config.TaskConfig, job domain.VideoJob, exchange domain.ModelDiagnostics) error {
	invalid := naming.FailureTitleError(result, current.FailureKeywords)
	var cleaned string
	if invalid == nil {
		cleaned, invalid = naming.CleanTitle(result, filepath.Ext(job.Path))
	}
	if invalid != nil {
		invalid.RawRequest, invalid.RawResponse = exchange.Request, exchange.Response
		return invalid
	}
	m.updateStage(itemID, func(s *LiveStage) { s.Title = cleaned + naming.NormalizeExtension(filepath.Ext(job.Path)) })
	return nil
}

// modelAttemptRecord is one finished model call of an item.
type modelAttemptRecord struct {
	item       int64
	number     int
	modelID    int64
	modelTitle string
	fallback   bool
	started    time.Time
	err        error
}

// recordModelAttempt saves the attempt and its token usage.
func (m *Manager) recordModelAttempt(ctx context.Context, run Run, a modelAttemptRecord, current *config.TaskConfig, exchange domain.ModelDiagnostics) error {
	status, message := "success", ""
	if a.err != nil {
		status, message = "failed", domain.RecordText(a.err)
	}
	if ctx.Err() != nil {
		status = "cancelled"
	}
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	if _, err := m.DB.ExecContext(ctx, `INSERT INTO model_attempts(item,attempt,model_config_id,model_config_title,model,fallback,started,duration_ms,status,message) VALUES(?,?,?,?,?,?,?,?,?,?)`, a.item, a.number, a.modelID, a.modelTitle, current.Server.Model, a.fallback, a.started.UTC().Format(time.RFC3339Nano), time.Since(a.started).Milliseconds(), status, message); err != nil {
		return fmt.Errorf("save model attempt: %w", err)
	}
	summary, _ := json.Marshal(map[string]string{"model": current.Server.Model, "provider": current.Server.Provider, "protocol": current.Server.Protocol})
	if err := tokenusage.RecordAttempt(ctx, m.DB, run.ID, a.item, a.number, a.modelID, a.modelTitle, string(summary), exchange.Request, exchange.Response, time.Now()); err != nil {
		return fmt.Errorf("save model usage: %w", err)
	}
	return nil
}

// retryBackoff honours a server Retry-After and otherwise doubles from 2s per
// slot up to 15s, with up to 50% jitter.
func retryBackoff(slot int, retryAfter time.Duration) time.Duration {
	base := min(15*time.Second, 2*time.Second*time.Duration(1<<slot))
	return max(retryAfter, time.Duration(float64(base)*(0.5+0.5*rand.Float64())))
}

// waitBeforeRetry sleeps for delay across pause/resume. A pause releases the
// screenshot lease so a long wait does not pin screenshots.
func (m *Manager) waitBeforeRetry(ctx context.Context, run Run, delay time.Duration, lease **framecache.Lease) error {
	until := time.Now().Add(delay)
	for time.Now().Before(until) {
		schedule, done := m.scheduleContext(ctx, run.ID)
		err := retryWait(schedule, time.Until(until))
		done()
		if err == nil {
			return nil
		}
		if *lease != nil {
			(*lease).Release(false)
			*lease = nil
		}
		if err := m.waitDispatch(ctx, run.ID); err != nil {
			return err
		}
	}
	return nil
}

// Attempt is the public, credential-free record shown in batch details.
type Attempt struct {
	Number           int           `json:"number"`
	ModelConfigID    int64         `json:"model_config_id"`
	ModelConfigTitle string        `json:"model_config_title"`
	Model            string        `json:"model"`
	Fallback         bool          `json:"fallback"`
	Started          string        `json:"started"`
	DurationMS       int64         `json:"duration_ms"`
	Status           string        `json:"status"`
	Error            domain.Record `json:"error,omitzero"`
}

// Attempts returns the recorded attempts of each item in items.
func Attempts(ctx context.Context, db *storage.Store, items []int64) (map[int64][]Attempt, error) {
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	rows, err := db.Read().QueryContext(ctx, "SELECT item,attempt,model_config_id,model_config_title,model,fallback,started,duration_ms,status,message FROM model_attempts WHERE item IN (SELECT value FROM json_each(?)) ORDER BY item,attempt", string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][]Attempt, len(items))
	for _, item := range items {
		out[item] = []Attempt{}
	}
	for rows.Next() {
		var a Attempt
		var item int64
		if err = rows.Scan(&item, &a.Number, &a.ModelConfigID, &a.ModelConfigTitle, &a.Model, &a.Fallback, &a.Started, &a.DurationMS, &a.Status, &a.Error); err != nil {
			return nil, err
		}
		out[item] = append(out[item], a)
	}
	return out, rows.Err()
}
