package app

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/systemsettings"
)

const numberingLease = 20 * time.Second

type numberingRequest struct {
	Key     string   `json:"key"`
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
	if len(req.Key) < 8 || len(req.Key) > 128 || strings.ContainsAny(req.Key, "/\\?#") ||
		(req.Action != "preview" && req.Action != "apply") || req.Parent < 1 ||
		(req.Action == "apply" && ((len(req.Batches) == 0 && req.Preview == "") || len(req.Batches) > 1000 || (len(req.Batches) > 0 && req.Preview != ""))) ||
		(req.Action == "preview" && (len(req.Batches) > 0 || req.Preview != "")) {
		invalid(w, r, "", "invalid_numbering_request")
		return
	}
	a.numberingMu.Lock()
	defer a.numberingMu.Unlock()
	if a.numberingClosed {
		writeError(w, r, errShuttingDown)
		return
	}
	for key, job := range a.numberingJobs {
		if !job.finished.IsZero() && time.Since(job.finished) > time.Hour {
			delete(a.numberingJobs, key)
		}
	}
	if job := a.numberingJobs[req.Key]; job != nil {
		if job.request.Action == "" {
			send(w, http.StatusOK, job.status)
			return
		}
		if !reflect.DeepEqual(job.request, req) {
			reject(w, r, domain.KindConflict, "request_key_reused", "request key is used by a different request")
			return
		}
		send(w, http.StatusOK, job.status)
		return
	}
	for _, job := range a.numberingJobs {
		if job.finished.IsZero() {
			reject(w, r, domain.KindConflict, "numbering_running", "another numbering job is running or stopping")
			return
		}
	}
	if len(a.numberingJobs) >= 64 {
		reject(w, r, domain.KindTooMany, "numbering_jobs_full", "too many recent numbering jobs")
		return
	}
	batchIDs := slices.Clone(req.Batches)
	if req.Preview != "" {
		source := a.numberingJobs[req.Preview]
		if source == nil || source.status.Status != "completed" || source.request.Action != "preview" || source.request.Parent != req.Parent {
			reject(w, r, domain.KindConflict, "preview_stale", "preview expired or belongs to another directory")
			return
		}
		for _, b := range source.result {
			if b.Changed > 0 {
				batchIDs = append(batchIDs, b.ID)
			}
		}
		if len(batchIDs) == 0 {
			reject(w, r, domain.KindInvalid, "nothing_to_number", "no files need numbering")
			return
		}
	}
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
func (a *App) runNumberingJob(ctx context.Context, job *numberingJob) {
	update := func(fn func(*numberingStatus)) { a.numberingMu.Lock(); fn(&job.status); a.numberingMu.Unlock() }
	var result []library.DirectoryNumbering
	var err error
	if job.request.Action == "preview" {
		var settings domain.NumberingSettings
		settings, err = (systemsettings.Store{DB: a.DB}).Numbering(ctx)
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
		reject(w, r, domain.KindNotFound, "numbering_job_gone", "numbering job expired or the service restarted")
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
			invalid(w, r, "key", "invalid")
			return
		}
		// A close can overtake the submission request on another connection.
		if a.numberingJobs == nil {
			a.numberingJobs = map[string]*numberingJob{}
		}
		if len(a.numberingJobs) < 64 {
			a.numberingJobs[key] = &numberingJob{finished: time.Now(), status: numberingStatus{Key: key, Status: "cancelled"}}
		}
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
		invalid(w, r, "offset", "invalid_number")
		return
	}
	a.numberingMu.Lock()
	job := a.numberingJobs[r.PathValue("key")]
	if job == nil || job.status.Status != "completed" {
		a.numberingMu.Unlock()
		reject(w, r, domain.KindConflict, "preview_not_ready", "preview not completed or expired")
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
