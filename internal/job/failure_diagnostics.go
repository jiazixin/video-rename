package job

import (
	"context"
	"database/sql"
	"errors"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// diagnosticPath allows a final path lookup after cancellation, bounded by the
// same deadline as final persistence so shutdown cannot wait indefinitely.
func (m *Manager) diagnosticPath(ctx context.Context, entry int64) string {
	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	return m.Library.DisplayPath(ctx, entry)
}

// recordFailureOptions groups the inputs for recordFailure.
type recordFailureOptions struct {
	Action string
	Entry  int64
	Run    int64
	Path   string
	Cause  error
}

// recordFailure keeps structured diagnostics at every rename failure boundary,
// including errors wrapped by scanning or run-level processing.
func (m *Manager) recordFailure(ctx context.Context, params recordFailureOptions) error {
	action, entry, run, path, cause := params.Action, params.Entry, params.Run, params.Path, params.Cause

	ctx, cancel := storage.Detach(ctx)
	defer cancel()
	var request, response, processing string
	if diagnostic, ok := errors.AsType[*domain.JobError](cause); ok {
		request, response = diagnostic.RawRequest, diagnostic.RawResponse
		processing = diagnostic.ProcessingLog + diagnostic.StderrTail
	}
	_, err := storage.WriteValue(ctx, m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "INSERT INTO events(category,action,entry,run,old_path,message,created,raw_request,raw_response,processing_log) VALUES('failure',?,?,?,?,?,?,?,?,?)", action, entry, run, path, domain.RecordText(cause), storage.Now(), request, response, processing)
	})
	return err
}
