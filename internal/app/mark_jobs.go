package app

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"video-rename/internal/library"
)

type markStatus struct {
	library.MarkResult
	Key    string `json:"key"`
	Status string `json:"status"`
}

type markJob struct {
	targets  []int64
	marked   bool
	status   markStatus
	finished time.Time
}

// startMark keeps the worker on the application's lifetime, independent of the
// HTTP request. Retrying the same key retrieves the same work and counts.
func (a *App) startMark(w http.ResponseWriter, r *http.Request, key string, targets []int64, marked bool) {
	if len(key) < 8 || len(key) > 128 || strings.ContainsAny(key, "/\\?#") {
		send(w, 400, map[string]string{"message": "标记请求键无效"})
		return
	}
	a.markMu.Lock()
	reply := func(status int, body any) {
		a.markMu.Unlock()
		send(w, status, body)
	}
	if a.markClosed || a.ctx.Err() != nil {
		reply(503, map[string]string{"message": "服务正在关闭，请稍后重试标记"})
		return
	}
	for id, job := range a.markJobs {
		if !job.finished.IsZero() && time.Since(job.finished) > time.Hour {
			delete(a.markJobs, id)
		}
	}
	if job, ok := a.markJobs[key]; ok {
		if job.marked != marked || !slices.Equal(job.targets, targets) {
			reply(409, map[string]string{"message": "标记请求键已用于其他目标或状态"})
			return
		}
		reply(200, job.status)
		return
	}
	// Serialize mark requests so a second tab cannot interleave opposite states.
	for _, job := range a.markJobs {
		if job.finished.IsZero() {
			reply(409, map[string]string{"message": "已有标记任务正在后台执行，请等待完成后重试"})
			return
		}
	}
	if len(a.markJobs) >= 128 {
		reply(429, map[string]string{"message": "近期标记任务过多，请稍后重试"})
		return
	}
	if a.markJobs == nil {
		a.markJobs = make(map[string]*markJob)
	}
	job := &markJob{targets: slices.Clone(targets), marked: marked, status: markStatus{
		Key: key, Status: "running", MarkResult: library.MarkResult{Failures: []library.MarkFailure{}},
	}}
	a.markJobs[key] = job
	a.markWG.Go(func() {
		started := time.Now()
		var last time.Time
		result := a.Library.SetProcessedWithProgress(a.ctx, job.targets, job.marked, func(result library.MarkResult) {
			if time.Since(last) < 200*time.Millisecond {
				return
			}
			a.markMu.Lock()
			job.status.MarkResult = result
			job.status.Failures = slices.Clone(result.Failures)
			a.markMu.Unlock()
			last = time.Now()
		})
		slog.Info("mark task finished", "key", key, "marked", marked,
			"elapsed", time.Since(started), "changed", result.Changed, "unchanged", result.Unchanged,
			"failed", result.Failed, "interrupted", result.Interrupted,
			"refresh_duration", result.Metrics.RefreshDuration, "validation_duration", result.Metrics.ValidationDuration,
			"refresh_readdir_duration", result.Metrics.RefreshReadDirectoryDuration,
			"refresh_metadata_duration", result.Metrics.RefreshMetadataDuration,
			"refresh_index_duration", result.Metrics.RefreshIndexDuration,
			"write_duration", result.Metrics.WriteDuration, "committed_batches", result.Metrics.CommittedBatches)
		a.markMu.Lock()
		defer a.markMu.Unlock()
		job.status.MarkResult = result
		job.status.Status = "completed"
		if result.Interrupted {
			job.status.Status = "interrupted"
		} else if result.Failed > 0 {
			job.status.Status = "partial_failure"
		}
		job.finished = time.Now()
	})
	reply(http.StatusAccepted, job.status)
}

func (a *App) markStatus(w http.ResponseWriter, r *http.Request) {
	a.markMu.Lock()
	job, ok := a.markJobs[r.PathValue("key")]
	if !ok || !job.finished.IsZero() && time.Since(job.finished) > time.Hour {
		a.markMu.Unlock()
		send(w, 404, map[string]string{"message": "标记任务状态已过期或服务已重启；已完成的标记仍保留，请重新选择目标后重试"})
		return
	}
	status := job.status
	a.markMu.Unlock()
	send(w, 200, status)
}
