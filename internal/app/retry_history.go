package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"

	"video-rename/internal/domain"
	"video-rename/internal/job"
	recordstore "video-rename/internal/record"
)

// retryHistory resubmits chosen history records as one batch on the chosen
// model's current settings.
func (a *App) retryHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Category      string  `json:"category"`
		IDs           []int64 `json:"ids"`
		All           bool    `json:"all"`
		Key           string  `json:"-"`
		ModelConfigID int64   `json:"model_config_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Key = requestKey(r)
	if input.Category == "" {
		input.Category = "failure"
	}
	if (input.Category != "failure" && input.Category != "success") || (input.Category == "success" && input.All) || (input.All && len(input.IDs) > 0) || (!input.All && len(input.IDs) == 0) || input.ModelConfigID < 0 || len(input.Key) < 8 || len(input.Key) > 120 {
		writeError(w, r, domain.InvalidField("", "invalid_retry_request"))
		return
	}
	var selectedRecords []retryRecord
	var problems []retryProblem
	var skipped int
	var err error
	if input.All {
		selectedRecords, skipped, err = recordstore.New(a.db).AllRetryableFailures(r.Context())
	} else {
		selectedRecords, problems, err = recordstore.New(a.db).ChosenRetryRecords(r.Context(), input.IDs, input.Category)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	targets, retryOperations, targetProblems := a.retryTargets(r.Context(), selectedRecords, input.Category)
	problems = append(problems, targetProblems...)
	accepted, runs := []int64{}, []int64{}
	if len(targets) > 0 {
		entries := make([]int64, 0, len(targets))
		for _, target := range targets {
			entries = append(entries, target.entry)
		}
		run, err := a.Jobs.Create(r.Context(), job.Request{Targets: entries, ModelConfigID: input.ModelConfigID, Key: input.Key, RetryOperations: retryOperations}, "manual", 0)
		if err != nil {
			problem := domain.RecordOf(err)
			if errors.Is(err, sql.ErrNoRows) {
				problem = domain.Record{Code: "retry_target_stale", Message: "retry targets are no longer indexed"}
			}
			problems = append(problems, retryProblem{Files: len(targets), Error: problem})
		} else {
			runs = append(runs, run)
			for _, target := range targets {
				accepted = append(accepted, target.records...)
			}
		}
	}
	if problems == nil {
		problems = []retryProblem{}
	}
	status := http.StatusOK
	if len(runs) > 0 {
		status = http.StatusAccepted
	}
	send(w, status, map[string]any{"accepted": accepted, "runs": runs, "errors": problems, "skipped": skipped})
}

// retryProblem is a record, or a group of Files, that was not resubmitted.
type retryProblem = recordstore.RetryProblem

// retryRecord is one history event to resubmit.
type retryRecord = recordstore.RetryRecord

// retryTarget is one file to rename again and the records it answers.
type retryTarget struct {
	entry   int64
	records []int64
}

// retryTargets groups records by file, keeping only files that still resolve.
// Success records also carry the operation each retry replaces.
func (a *App) retryTargets(ctx context.Context, records []retryRecord, category string) (targets []*retryTarget, operations map[int64]int64, problems []retryProblem) {
	operations = map[int64]int64{}
	byEntry := map[int64]*retryTarget{}
	for _, record := range records {
		if category == "success" {
			if err := a.Library.CheckAutomaticRetry(ctx, record.Entry, record.Operation); err != nil {
				problems = append(problems, retryProblem{Record: record.ID, Error: domain.RecordOf(err)})
				continue
			}
		}
		if target, ok := byEntry[record.Entry]; ok {
			target.records = append(target.records, record.ID)
			continue
		}
		if _, err := a.Library.Path(ctx, record.Entry); err != nil {
			problems = append(problems, retryProblem{Record: record.ID, Error: retryPathProblem(err)})
			continue
		}
		target := &retryTarget{entry: record.Entry, records: []int64{record.ID}}
		byEntry[record.Entry] = target
		targets = append(targets, target)
		if category == "success" {
			operations[record.Entry] = record.Operation
		}
	}
	return targets, operations, problems
}

func retryPathProblem(err error) domain.Record {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Record{Code: "entry_not_indexed", Message: "file or parent directory is no longer indexed"}
	case errors.Is(err, os.ErrNotExist):
		return domain.Record{Code: "entry_missing", Message: "file or parent directory does not exist"}
	default:
		return domain.RecordOf(err)
	}
}
