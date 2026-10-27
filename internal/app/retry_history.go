package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"

	"video-rename/internal/domain"
	"video-rename/internal/jobs"
)

// retryHistory resubmits chosen history records as one batch on the chosen
// model's current settings.
func (a *App) retryHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Category      string  `json:"category"`
		IDs           []int64 `json:"ids"`
		All           bool    `json:"all"`
		Key           string  `json:"key"`
		ModelConfigID int64   `json:"model_config_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Category == "" {
		input.Category = "failure"
	}
	if (input.Category != "failure" && input.Category != "success") || (input.Category == "success" && input.All) || (input.All && len(input.IDs) > 0) || (!input.All && len(input.IDs) == 0) || input.ModelConfigID < 0 || len(input.Key) < 8 || len(input.Key) > 120 {
		invalid(w, r, "", "invalid_retry_request")
		return
	}
	var records []retryRecord
	var problems []retryProblem
	var skipped int
	var err error
	if input.All {
		records, skipped, err = a.allRetryableFailures(r.Context())
	} else {
		records, problems, err = a.chosenRetryRecords(r.Context(), input.IDs, input.Category)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	targets, retryOperations, targetProblems := a.retryTargets(r.Context(), records, input.Category)
	problems = append(problems, targetProblems...)
	accepted, runs := []int64{}, []int64{}
	if len(targets) > 0 {
		entries := make([]int64, 0, len(targets))
		for _, target := range targets {
			entries = append(entries, target.entry)
		}
		run, err := a.Jobs.Create(r.Context(), jobs.Request{Targets: entries, ModelConfigID: input.ModelConfigID, Key: input.Key, RetryOperations: retryOperations}, "manual", 0)
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
	send(w, http.StatusOK, map[string]any{"accepted": accepted, "runs": runs, "errors": problems, "skipped": skipped})
}

// retryProblem is a record, or a group of Files, that was not resubmitted.
type retryProblem struct {
	Record int64         `json:"record,omitzero"`
	Files  int           `json:"files,omitzero"`
	Error  domain.Record `json:"error"`
}

// retryRecord is one history event to resubmit.
type retryRecord struct{ id, entry, operation int64 }

// retryTarget is one file to rename again and the records it answers.
type retryTarget struct {
	entry   int64
	records []int64
}

// allRetryableFailures lists every automatic failure with a file; other
// failures are counted as skipped.
func (a *App) allRetryableFailures(ctx context.Context) (records []retryRecord, skipped int, err error) {
	rows, err := a.DB.Read().QueryContext(ctx, "SELECT id,entry,action FROM events WHERE category='failure' ORDER BY id DESC")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var record retryRecord
		var action string
		if err = rows.Scan(&record.id, &record.entry, &action); err != nil {
			return nil, 0, err
		}
		if action != "automatic" || record.entry <= 0 {
			skipped++
			continue
		}
		records = append(records, record)
	}
	return records, skipped, rows.Err()
}

// chosenRetryRecords loads the chosen automatic records of one category and
// reports the ones that cannot be retried.
func (a *App) chosenRetryRecords(ctx context.Context, ids []int64, category string) (records []retryRecord, problems []retryProblem, err error) {
	for _, id := range ids {
		record := retryRecord{id: id}
		err := a.DB.Read().QueryRowContext(ctx, "SELECT entry,operation FROM events WHERE id=? AND category=? AND action='automatic' AND entry>0", id, category).Scan(&record.entry, &record.operation)
		if errors.Is(err, sql.ErrNoRows) {
			problems = append(problems, retryProblem{Record: id, Error: domain.Record{Code: "record_not_retryable", Message: "record does not exist or cannot be retried automatically"}})
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}
	return records, problems, nil
}

// retryTargets groups records by file, keeping only files that still resolve.
// Success records also carry the operation each retry replaces.
func (a *App) retryTargets(ctx context.Context, records []retryRecord, category string) (targets []*retryTarget, operations map[int64]int64, problems []retryProblem) {
	operations = map[int64]int64{}
	byEntry := map[int64]*retryTarget{}
	for _, record := range records {
		if category == "success" {
			if err := a.Library.CheckAutomaticRetry(ctx, record.entry, record.operation); err != nil {
				problems = append(problems, retryProblem{Record: record.id, Error: domain.RecordOf(err)})
				continue
			}
		}
		if target, ok := byEntry[record.entry]; ok {
			target.records = append(target.records, record.id)
			continue
		}
		if _, err := a.Library.Path(record.entry); err != nil {
			problems = append(problems, retryProblem{Record: record.id, Error: retryPathProblem(err)})
			continue
		}
		target := &retryTarget{entry: record.entry, records: []int64{record.id}}
		byEntry[record.entry] = target
		targets = append(targets, target)
		if category == "success" {
			operations[record.entry] = record.operation
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
