package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
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
		send(w, 400, map[string]string{"message": "请选择要重试的记录，并提供有效模型配置和请求键；成功记录仅支持明确选择后重试"})
		return
	}
	var records []retryRecord
	var problems []string
	var skipped int
	var err error
	if input.All {
		records, skipped, err = a.allRetryableFailures(r.Context())
	} else {
		records, problems, err = a.chosenRetryRecords(r.Context(), input.IDs, input.Category)
	}
	if err != nil {
		failure(w, err)
		return
	}
	targets, retryOperations, targetProblems := a.retryTargets(records, input.Category)
	problems = append(problems, targetProblems...)
	accepted, runs := []int64{}, []int64{}
	if len(targets) > 0 {
		entries := make([]int64, 0, len(targets))
		for _, target := range targets {
			entries = append(entries, target.entry)
		}
		run, err := a.Jobs.Create(jobs.Request{Targets: entries, ModelConfigID: input.ModelConfigID, Key: input.Key, RetryOperations: retryOperations}, "manual", 0)
		if err != nil {
			message := err.Error()
			if errors.Is(err, sql.ErrNoRows) {
				message = "重试目标的索引已失效，请到文件目录刷新并重新提交命名"
			}
			problems = append(problems, fmt.Sprintf("%d 个文件未提交：%s", len(targets), message))
		} else {
			runs = append(runs, run)
			for _, target := range targets {
				accepted = append(accepted, target.records...)
			}
		}
	}
	if problems == nil {
		problems = []string{}
	}
	send(w, 200, map[string]any{"accepted": accepted, "runs": runs, "errors": problems, "skipped": skipped})
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
func (a *App) chosenRetryRecords(ctx context.Context, ids []int64, category string) (records []retryRecord, problems []string, err error) {
	for _, id := range ids {
		record := retryRecord{id: id}
		err := a.DB.Read().QueryRowContext(ctx, "SELECT entry,operation FROM events WHERE id=? AND category=? AND action='automatic' AND entry>0", id, category).Scan(&record.entry, &record.operation)
		if errors.Is(err, sql.ErrNoRows) {
			problems = append(problems, fmt.Sprintf("记录 #%d 不存在或不支持自动重试", id))
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
func (a *App) retryTargets(records []retryRecord, category string) (targets []*retryTarget, operations map[int64]int64, problems []string) {
	operations = map[int64]int64{}
	byEntry := map[int64]*retryTarget{}
	for _, record := range records {
		if category == "success" {
			if err := a.Library.CheckAutomaticRetry(record.entry, record.operation); err != nil {
				problems = append(problems, fmt.Sprintf("记录 #%d：%s", record.id, err))
				continue
			}
		}
		if target, ok := byEntry[record.entry]; ok {
			target.records = append(target.records, record.id)
			continue
		}
		if _, err := a.Library.Path(record.entry); err != nil {
			problems = append(problems, fmt.Sprintf("记录 #%d：%s", record.id, retryPathProblem(err)))
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

func retryPathProblem(err error) string {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "文件或上级目录已不在索引中，请到文件目录刷新并重新提交命名；若文件已删除，可清除对应记录"
	case errors.Is(err, os.ErrNotExist):
		return "文件或上级目录不存在，请检查媒体挂载并刷新所在目录后重试；若文件已删除，请清除对应记录"
	default:
		return err.Error()
	}
}
