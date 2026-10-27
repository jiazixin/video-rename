package app

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"video-rename/internal/domain"
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
	cancel   context.CancelFunc
	finished time.Time
}

// startMark detaches the worker from the HTTP request; Shutdown cancels it.
// While a result is cached, the same key returns its work and counts. Clients retry existing requests through markStatus: a retired result must not
// be mistaken for a new operation and replayed over a later opposite mark.
func (a *App) startMark(w http.ResponseWriter, r *http.Request, key string, targets []int64, marked bool) {
	if len(key) < 8 || len(key) > 128 || strings.ContainsAny(key, "/\\?#") {
		invalid(w, r, "key", "invalid")
		return
	}
	a.markMu.Lock()
	reply := func(status int, body any) {
		a.markMu.Unlock()
		send(w, status, body)
	}
	fail := func(err error) {
		a.markMu.Unlock()
		writeError(w, r, err)
	}
	if a.markClosed {
		fail(errShuttingDown)
		return
	}
	maps.DeleteFunc(a.markJobs, func(_ string, job *markJob) bool {
		return !job.finished.IsZero() && time.Since(job.finished) > markResultRetention
	})
	if job, ok := a.markJobs[key]; ok {
		if job.marked != marked || !slices.Equal(job.targets, targets) {
			fail(domain.Fail(domain.KindConflict, "request_key_reused", "request key is used by a different request"))
			return
		}
		reply(200, job.status)
		return
	}
	// Serialize mark requests so a second tab cannot interleave opposite states.
	for _, job := range a.markJobs {
		if job.finished.IsZero() {
			fail(domain.Fail(domain.KindConflict, "mark_running", "another mark job is running"))
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
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	job := &markJob{targets: slices.Clone(targets), marked: marked, cancel: cancel, status: markStatus{
		Key: key, Status: "running", Failures: []library.MarkFailure{},
	}}
	a.markJobs[key] = job
	a.markWG.Go(func() {
		defer cancel()
		var last time.Time
		result := a.Library.SetProcessedWithProgress(ctx, job.targets, job.marked, func(result library.MarkResult) {
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
		reject(w, r, domain.KindNotFound, "mark_result_gone", "mark result expired or the service restarted")
		return
	}
	status := job.status
	a.markMu.Unlock()
	send(w, http.StatusOK, status)
}
