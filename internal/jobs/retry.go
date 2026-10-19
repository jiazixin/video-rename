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

func (m *Manager) freezeRetry(task string, primary int64) (retryPolicy, error) {
	if task == "extract" {
		return retryPolicy{}, nil
	}
	settings, err := (systemsettings.Store{DB: m.DB}).Get()
	if err != nil {
		return retryPolicy{}, err
	}
	keywords, err := (systemsettings.Store{DB: m.DB}).FailureKeywords()
	if err != nil {
		return retryPolicy{}, err
	}
	p := retryPolicy{Enabled: settings.RetryEnabled, Count: settings.RetryCount, FailureKeywords: keywords}
	if !p.Enabled || settings.FallbackModelID == 0 || settings.FallbackModelID == primary {
		return p, nil
	}
	model, err := (models.Store{DB: m.DB}).Get(settings.FallbackModelID)
	if err != nil {
		return p, fmt.Errorf("兜底模型不可用：%w", err)
	}
	if err = model.Validate(); err != nil {
		return p, fmt.Errorf("兜底模型配置无效：%w", err)
	}
	fixed, err := (systemsettings.Store{DB: m.DB}).FixedPrompt()
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

func (m *Manager) inferWithRetry(ctx context.Context, run Run, entry int64, cfg *config.TaskConfig, job domain.VideoJob, probe, ffmpeg string, leaseRef **framecache.Lease) (string, error) {
	lease := *leaseRef
	defer func() { *leaseRef = lease }()
	var itemID int64
	if err := m.DB.QueryRow("SELECT id FROM run_items WHERE run=? AND entry=?", run.ID, entry).Scan(&itemID); err != nil {
		return "", err
	}
	total := 1
	if run.retry.Enabled {
		total += run.retry.Count
	}
	useFallback := false
	attempt := 0
	for slot := 0; slot < total; slot++ {
		current := *cfg
		id, title := run.ModelConfigID, run.ModelConfigTitle
		fallback := run.retry.FallbackID > 0 && (useFallback || slot == total-1) && slot > 0
		if fallback {
			if err := (models.Store{DB: m.DB}).ApplySnapshot(run.retry.FallbackSnapshot, &current); err != nil {
				return "", err
			}
			id, title = run.retry.FallbackID, run.retry.FallbackTitle
		}
		m.updateStage(itemID, func(s *LiveStage) {
			s.Stage, s.Attempt, s.Fallback = "model_wait", attempt+1, fallback
			if lease != nil {
				s.Frames = len(lease.Set.Frames)
			}
		})
		waitStarted := time.Now()
		release, n, err := m.inferenceSlot(ctx, run, entry, id, &current, job, probe, ffmpeg, &lease)
		if err != nil {
			return "", err
		}
		current.Workers, current.Server.Workers = n, n
		snap, err := domain.StatSnapshot(job.Path)
		if err == nil && !snap.Same(job.Snapshot) {
			diagnostic := domain.SourceChangedError("视频在等待模型期间发生变化", "抽帧完成后、获取模型名额后、调用模型前", job.Path, job.Snapshot, snap)
			diagnostic.ProcessingLog += fmt.Sprintf("批次：%d\n文件条目：%d\n模型排队时长：%s\n基准：本视频处理开始时的原文件快照；当前：取得模型名额后的原文件快照。\n本次尚未调用模型；校验对象不是截图目录或截图数量。\n", run.ID, entry, time.Since(waitStarted))
			if lease != nil {
				diagnostic.ProcessingLog += fmt.Sprintf("已准备截图：%d 张\n", len(lease.Set.Frames))
			}
			err = diagnostic
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
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
			invalid := naming.FailureTitleError(result, current.FailureKeywords)
			var cleaned string
			if invalid == nil {
				cleaned, invalid = naming.CleanTitle(result, filepath.Ext(job.Path))
			}
			if invalid == nil {
				m.updateStage(itemID, func(s *LiveStage) { s.Title = cleaned + naming.NormalizeExtension(filepath.Ext(job.Path)) })
			}
			if invalid != nil {
				invalid.RawRequest, invalid.RawResponse = exchange.Request, exchange.Response
				err = invalid
			}
		}
		status, message := "success", ""
		if err != nil {
			status, message = "failed", err.Error()
			if diagnostic, ok := errors.AsType[*domain.JobError](err); ok {
				exchange.Request, exchange.Response = diagnostic.RawRequest, diagnostic.RawResponse
			}
		}
		if ctx.Err() != nil {
			status = "cancelled"
		}
		if _, recordErr := m.DB.Exec(`INSERT INTO model_attempts(item,attempt,model_config_id,model_config_title,model,fallback,started,duration_ms,status,message) VALUES(?,?,?,?,?,?,?,?,?,?)`, itemID, attempt, id, title, current.Server.Model, fallback, started.UTC().Format(time.RFC3339Nano), time.Since(started).Milliseconds(), status, message); recordErr != nil {
			return "", fmt.Errorf("保存模型尝试记录：%w", recordErr)
		}
		summary, _ := json.Marshal(map[string]string{"model": current.Server.Model, "provider": current.Server.Provider, "protocol": current.Server.Protocol})
		if recordErr := tokenusage.RecordAttempt(m.DB, run.ID, itemID, attempt, id, title, string(summary), exchange.Request, exchange.Response, time.Now()); recordErr != nil {
			return "", fmt.Errorf("保存模型用量：%w", recordErr)
		}
		if err == nil {
			domain.CaptureModelDiagnostics(ctx, domain.TitleResult{RawRequest: exchange.Request, RawResponse: exchange.Response})
			return result, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		again, canFallback, delay := retryKind(err)
		if fallback || slot == total-1 || (!again && (!canFallback || run.retry.FallbackID == 0)) {
			return "", err
		}
		if !again {
			useFallback = true
			slot = total - 2
			continue
		}
		m.setStage(itemID, "retry_wait")
		delay = max(delay, time.Duration(float64(min(15*time.Second, time.Second*2*time.Duration(1<<slot)))*(0.5+0.5*rand.Float64())))
		// Preserve Retry-After across pause/resume, without pinning screenshots.
		until := time.Now().Add(delay)
		for time.Now().Before(until) {
			if waitErr := retryWait(m.currentSchedule(ctx, run.ID), time.Until(until)); waitErr == nil {
				break
			}
			if lease != nil {
				lease.Release(false)
				lease = nil
			}
			if _, waitErr := m.dispatchContext(ctx, run.ID); waitErr != nil {
				return "", waitErr
			}
		}
	}
	return "", fmt.Errorf("模型请求未完成")
}

// Attempt is the public, credential-free record shown in batch details.
type Attempt struct {
	Number           int    `json:"number"`
	ModelConfigID    int64  `json:"model_config_id"`
	ModelConfigTitle string `json:"model_config_title"`
	Model            string `json:"model"`
	Fallback         bool   `json:"fallback"`
	Started          string `json:"started"`
	DurationMS       int64  `json:"duration_ms"`
	Status           string `json:"status"`
	Message          string `json:"message"`
}

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
		if err = rows.Scan(&item, &a.Number, &a.ModelConfigID, &a.ModelConfigTitle, &a.Model, &a.Fallback, &a.Started, &a.DurationMS, &a.Status, &a.Message); err != nil {
			return nil, err
		}
		out[item] = append(out[item], a)
	}
	return out, rows.Err()
}
