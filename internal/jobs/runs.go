package jobs

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"

	"video-rename/internal/domain"
)

// Run is one batch with its settings and progress.
type Run struct {
	retry            retryPolicy
	Task             string         `json:"task"`
	TargetNames      []string       `json:"target_names,omitempty"`
	RequestConfig    jsontext.Value `json:"request_config,omitempty"`
	ModelConfigID    int64          `json:"model_config_id"`
	ModelConfigTitle string         `json:"model_config_title"`
	PromptBody       string         `json:"prompt_body"`
	ID               int64          `json:"id"`
	Source           string         `json:"source"`
	Targets          []int64        `json:"targets"`
	Recursive        bool           `json:"recursive"`
	Status           string         `json:"status"`
	Created          string         `json:"created"`
	Started          string         `json:"started"`
	Finished         string         `json:"finished"`
	Discovered       int            `json:"discovered"`
	Done             int            `json:"done"`
	Skipped          int            `json:"skipped"`
	Failed           int            `json:"failed"`
	TokenCount       int64          `json:"token_count"`
	TokenRecords     int            `json:"token_records"`
	TokenEstimated   int            `json:"token_estimated_records"`
	Error            domain.Record  `json:"error,omitzero"`
	Plan             int64          `json:"plan"`
}

// Request is a request to create a batch.
type Request struct {
	// Set only by the history retry handler; generic run JSON cannot bypass marks.
	RetryOperations map[int64]int64 `json:"-"`
	Task            string          `json:"task"`
	ModelConfigID   int64           `json:"model_config_id"`
	Targets         []int64         `json:"targets"`
	Recursive       bool            `json:"recursive"`
	Key             string          `json:"key"`
}

const runColumns = "r.id,r.source,r.targets,r.recursive,r.status,r.created,r.finished,r.discovered,r.done,r.skipped,r.failed,r.error,r.plan,r.model_config_id,r.model_config_title,r.prompt_body,r.config_summary,r.started,COALESCE(u.token_count,0),COALESCE(u.token_records,0),COALESCE(u.token_estimated_records,0),r.task"

func scanRun(row interface{ Scan(...any) error }) (r Run, err error) {
	var raw, summary string
	err = row.Scan(&r.ID, &r.Source, &raw, &r.Recursive, &r.Status, &r.Created, &r.Finished, &r.Discovered, &r.Done, &r.Skipped, &r.Failed, &r.Error, &r.Plan, &r.ModelConfigID, &r.ModelConfigTitle, &r.PromptBody, &summary, &r.Started, &r.TokenCount, &r.TokenRecords, &r.TokenEstimated, &r.Task)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &r.Targets)
		if jsontext.Value(summary).IsValid() {
			r.RequestConfig = jsontext.Value(summary)
		}
	}
	return
}

// Get returns batch id.
func (m *Manager) Get(ctx context.Context, id int64) (Run, error) {
	r, err := scanRun(m.DB.Read().QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs r LEFT JOIN run_token_usage u ON u.run=r.id WHERE r.id=?", id))
	if err == nil {
		var paths map[int64]string
		paths, err = m.Library.DisplayPaths(ctx, r.Targets)
		for _, target := range r.Targets {
			r.TargetNames = append(r.TargetNames, paths[target])
		}
	}
	return r, err
}

// List returns the batches with IDs below before, newest first.
func (m *Manager) List(ctx context.Context, before int64) ([]Run, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := m.DB.Read().QueryContext(ctx, "SELECT "+runColumns+" FROM runs r LEFT JOIN run_token_usage u ON u.run=r.id WHERE r.id<? ORDER BY r.id DESC LIMIT 50", before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
