package app

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"video-rename/internal/library"
)

const (
	markResultCacheCapacity = 128
	markResultRetention     = time.Hour
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
// HTTP request. While a result is cached, the same key returns its work and counts.
// Clients retry existing requests through markStatus: a retired result must not
// be mistaken for a new operation and replayed over a later opposite mark.
func (a *App) startMark(w http.ResponseWriter, key string, targets []int64, marked bool) {
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
	maps.DeleteFunc(a.markJobs, func(_ string, job *markJob) bool {
		return !job.finished.IsZero() && time.Since(job.finished) > markResultRetention
	})
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
	// Completed results bound memory, not the number of user operations. Active
	// work was checked above; only the oldest completed result is reclaimed.
	for len(a.markJobs) >= markResultCacheCapacity {
		var oldestKey string
		var oldest time.Time
		for id, job := range a.markJobs {
			if !job.finished.IsZero() && (oldest.IsZero() || job.finished.Before(oldest)) {
				oldestKey, oldest = id, job.finished
			}
		}
		delete(a.markJobs, oldestKey)
	}
	if a.markJobs == nil {
		a.markJobs = make(map[string]*markJob)
	}
	job := &markJob{targets: slices.Clone(targets), marked: marked, status: markStatus{
		Key: key, Status: "running", Failures: []library.MarkFailure{},
	}}
	a.markJobs[key] = job
	a.markWG.Go(func() {
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
	key := r.PathValue("key")
	job, ok := a.markJobs[key]
	if !ok || !job.finished.IsZero() && time.Since(job.finished) > markResultRetention {
		delete(a.markJobs, key)
		a.markMu.Unlock()
		send(w, 404, map[string]string{"message": "标记任务结果不可查询，可能已回收、过期或服务已重启；已完成的标记仍保留，请重新选择目标后重试"})
		return
	}
	status := job.status
	a.markMu.Unlock()
	send(w, 200, status)
}
