package app

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/systemsetting"
)

const (
	// numberingLease stops a job whose client stopped asking for its status.
	numberingLease = 20 * time.Second
	// numberingJobCapacity bounds retained results; one job runs at a time.
	numberingJobCapacity  = 64
	numberingJobRetention = time.Hour
	// A cancel that overtook its submission only has to outlive that request.
	numberingCancelRetention = time.Minute
)

type numberingRequest struct {
	Key     string   `json:"-"`
	Action  string   `json:"action"`
	Parent  int64    `json:"parent"`
	Batches []string `json:"batches"`
	Preview string   `json:"preview"`
}

// numberingStatus reports a numbering job. During preview Directory is the
// directory being scanned; during apply DirectoryDone of DirectoryTotal files
// of the current directory are renamed.
type numberingStatus struct {
	Key            string        `json:"key"`
	Status         string        `json:"status"`
	Phase          string        `json:"phase"`
	Directory      int64         `json:"directory,omitzero"`
	DirectoryDone  int           `json:"directory_done,omitzero"`
	DirectoryTotal int           `json:"directory_total,omitzero"`
	Directories    int           `json:"directories"`
	Files          int           `json:"files"`
	Done           int           `json:"done"`
	Total          int           `json:"total"`
	Error          domain.Record `json:"error,omitzero"`
	ProcessingLog  string        `json:"processing_log"`
}
type numberingJob struct {
	request  numberingRequest
	batches  []string
	status   numberingStatus
	cancel   context.CancelFunc
	lease    time.Time
	finished time.Time
	result   []library.DirectoryNumbering
}

func (a *App) numberingJobRoutes(api *router) {
	api.HandleFunc("POST /api/numbering/jobs", a.startNumberingJob)
	api.HandleFunc("GET /api/numbering/jobs/{key}", a.numberingJobStatus)
	api.HandleFunc("POST /api/numbering/jobs/{key}/cancel", a.cancelNumberingJob)
	api.HandleFunc("GET /api/numbering/jobs/{key}/result", a.numberingJobResult)
}
func (a *App) startNumberingJob(w http.ResponseWriter, r *http.Request) {
	var req numberingRequest
	if !decode(w, r, &req) {
		return
	}
	req.Key = requestKey(r)
	if len(req.Key) < 8 || len(req.Key) > 128 || strings.ContainsAny(req.Key, "/\\?#") ||
		(req.Action != "preview" && req.Action != "apply") || req.Parent < 1 ||
		(req.Action == "apply" && ((len(req.Batches) == 0 && req.Preview == "") || len(req.Batches) > 1000 || (len(req.Batches) > 0 && req.Preview != ""))) ||
		(req.Action == "preview" && (len(req.Batches) > 0 || req.Preview != "")) {
		writeError(w, r, domain.InvalidField("", "invalid_numbering_request"))
		return
	}
	a.numberingMu.Lock()
	defer a.numberingMu.Unlock()
	if a.numberingClosed {
		writeError(w, r, errShuttingDown)
		return
	}
	a.pruneNumberingJobs(time.Now())
	if job := a.numberingJobs[req.Key]; job != nil {
		if job.request.Action == "" {
			send(w, http.StatusOK, job.status)
			return
		}
		if !reflect.DeepEqual(job.request, req) {
			writeError(w, r, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonIdempotencyKeyReused, Message: "request key is used by a different request"}))
			return
		}
		send(w, http.StatusOK, job.status)
		return
	}
	for _, job := range a.numberingJobs {
		if job.finished.IsZero() {
			writeError(w, r, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonNumberingRunning, Message: "another numbering job is running or stopping"}))
			return
		}
	}
	batchIDs := slices.Clone(req.Batches)
	if req.Preview != "" {
		source := a.numberingJobs[req.Preview]
		if source == nil || source.status.Status != "completed" || source.request.Action != "preview" || source.request.Parent != req.Parent {
			writeError(w, r, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonPreviewStale, Message: "preview expired or belongs to another directory"}))
			return
		}
		for _, b := range source.result {
			if b.Changed > 0 {
				batchIDs = append(batchIDs, b.ID)
			}
		}
		if len(batchIDs) == 0 {
			writeError(w, r, domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonNothingToNumber, Message: "no files need numbering"}))
			return
		}
	}
	// The preview was read above, so it may now be the result that makes room.
	a.evictNumberingJobs()
	// Detached from the request; cancel, the lease watchdog and Shutdown stop it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	job := &numberingJob{request: req, batches: batchIDs, cancel: cancel, lease: time.Now(), status: numberingStatus{Key: req.Key, Status: "running", Phase: req.Action}}
	if a.numberingJobs == nil {
		a.numberingJobs = map[string]*numberingJob{}
	}
	a.numberingJobs[req.Key] = job
	// Both workers are registered under the same mutex used by Close.
	a.numberingWG.Go(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.numberingMu.Lock()
				if job.finished.IsZero() && time.Since(job.lease) > numberingLease {
					job.status.Status = "cancelling"
					cancel()
				}
				a.numberingMu.Unlock()
			}
		}
	})
	a.numberingWG.Go(func() { a.runNumberingJob(ctx, job); cancel() })
	send(w, http.StatusAccepted, job.status)
}

// pruneNumberingJobs drops expired results. Callers hold numberingMu.
func (a *App) pruneNumberingJobs(now time.Time) {
	maps.DeleteFunc(a.numberingJobs, func(_ string, job *numberingJob) bool {
		if job.finished.IsZero() {
			return false
		}
		retention := numberingJobRetention
		if job.request.Action == "" {
			retention = numberingCancelRetention
		}
		return now.Sub(job.finished) > retention
	})
}

// evictNumberingJobs makes room for one more job by dropping the oldest
// finished results; a running job is never dropped. A preview evicted here
// answers a later apply with preview_stale. Callers hold numberingMu.
func (a *App) evictNumberingJobs() {
	for len(a.numberingJobs) >= numberingJobCapacity {
		var oldestKey string
		var oldest time.Time
		for key, job := range a.numberingJobs {
			if !job.finished.IsZero() && (oldest.IsZero() || job.finished.Before(oldest)) {
				oldestKey, oldest = key, job.finished
			}
		}
		if oldestKey == "" {
			return
		}
		delete(a.numberingJobs, oldestKey)
	}
}

func (a *App) runNumberingJob(ctx context.Context, job *numberingJob) {
	update := func(fn func(*numberingStatus)) { a.numberingMu.Lock(); fn(&job.status); a.numberingMu.Unlock() }
	var result []library.DirectoryNumbering
	var err error
	if job.request.Action == "preview" {
		var settings domain.NumberingSettings
		settings, err = (systemsetting.New(a.db)).Numbering(ctx)
		if err == nil {
			result, err = a.Library.PreviewNumberingTreeWithProgress(ctx, job.request.Parent, settings, func(id int64, dirs, files int) {
				update(func(s *numberingStatus) {
					s.Directory = id
					s.Directories = dirs
					s.Files = files
				})
			})
		}
	} else {
		update(func(s *numberingStatus) { s.Total = len(job.batches) })
		for _, id := range job.batches {
			if err = ctx.Err(); err != nil {
				break
			}
			var b library.Numbering
			b, err = a.Library.ApplyNumbering(ctx, id, func(done, total int) {
				update(func(s *numberingStatus) {
					s.Phase = "apply"
					s.DirectoryDone, s.DirectoryTotal = done, total
				})
			})
			if err != nil {
				break
			}
			update(func(s *numberingStatus) { s.Done++; s.Files += b.Changed })
		}
	}
	a.numberingMu.Lock()
	defer a.numberingMu.Unlock()
	job.finished = time.Now()
	job.status.Status = "completed"
	if err != nil {
		job.status.Status = "failed"
		job.status.Error = domain.RecordOf(err)
		if ctx.Err() != nil {
			job.status.Status = "cancelled"
		}
		if diagnostic, ok := errors.AsType[*domain.JobError](err); ok {
			job.status.ProcessingLog = diagnostic.ProcessingLog
		}
		if job.status.Status == "failed" {
			a.logger.Warn("numbering_failed", "error", err, "diagnostic", job.status.ProcessingLog)
		}
	} else if ctx.Err() != nil && job.request.Action == "preview" {
		job.status.Status = "cancelled"
	} else {
		job.result = result
	}
}
func (a *App) numberingJobStatus(w http.ResponseWriter, r *http.Request) {
	a.numberingMu.Lock()
	defer a.numberingMu.Unlock()
	job := a.numberingJobs[r.PathValue("key")]
	if job == nil {
		writeError(w, r, domain.Fail(domain.KindNotFound, domain.Failure{Reason: domain.ReasonNumberingJobGone, Message: "numbering job expired or the service restarted"}))
		return
	}
	job.lease = time.Now()
	send(w, http.StatusOK, job.status)
}
func (a *App) cancelNumberingJob(w http.ResponseWriter, r *http.Request) {
	a.numberingMu.Lock()
	defer a.numberingMu.Unlock()
	job := a.numberingJobs[r.PathValue("key")]
	if job == nil {
		key := r.PathValue("key")
		if len(key) < 8 || len(key) > 128 || strings.ContainsAny(key, "/\\?#") {
			writeError(w, r, domain.InvalidField("key", "invalid"))
			return
		}
		// A close can overtake the submission request on another connection.
		if a.numberingJobs == nil {
			a.numberingJobs = map[string]*numberingJob{}
		}
		a.pruneNumberingJobs(time.Now())
		a.evictNumberingJobs()
		a.numberingJobs[key] = &numberingJob{finished: time.Now(), status: numberingStatus{Key: key, Status: "cancelled"}}
		send(w, http.StatusOK, map[string]string{"status": "cancelled"})
		return
	}
	if job.finished.IsZero() {
		job.status.Status = "cancelling"
		job.cancel()
	}
	send(w, http.StatusOK, job.status)
}

type numberingPreviewItem struct {
	library.NumberingItem
	Path string `json:"path"`
}

func (a *App) numberingJobResult(w http.ResponseWriter, r *http.Request) {
	offset := int(number(r.URL.Query().Get("offset")))
	if offset < 0 {
		writeError(w, r, domain.InvalidField("offset", "invalid_number"))
		return
	}
	a.numberingMu.Lock()
	job := a.numberingJobs[r.PathValue("key")]
	if job == nil || job.status.Status != "completed" {
		a.numberingMu.Unlock()
		writeError(w, r, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonPreviewNotReady, Message: "preview not completed or expired"}))
		return
	}
	batches := slices.Clone(job.result)
	a.numberingMu.Unlock()
	items := []numberingPreviewItem{}
	total := 0
	for i := range batches {
		b := &batches[i]
		for j := max(0, offset-total); j < len(b.Items) && len(items) < 100; j++ {
			items = append(items, numberingPreviewItem{NumberingItem: b.Items[j], Path: b.Path})
		}
		total += len(b.Items)
		b.Items = nil
	}
	send(w, http.StatusOK, map[string]any{"batches": batches, "items": items, "total": total})
}
