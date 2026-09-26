package jobs

import (
	"errors"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// recordFailure keeps structured diagnostics at every rename failure boundary,
// including errors wrapped by scanning or run-level processing.
func (m *Manager) recordFailure(action string, entry, run int64, path string, cause error) error {
	var request, response, processing string
	if diagnostic, ok := errors.AsType[*domain.JobError](cause); ok {
		request, response = diagnostic.RawRequest, diagnostic.RawResponse
		processing = diagnostic.ProcessingLog + diagnostic.StderrTail
	}
	_, err := m.DB.Exec("INSERT INTO events(category,action,entry,run,old_path,message,created,raw_request,raw_response,processing_log) VALUES('failure',?,?,?,?,?,?,?,?,?)", action, entry, run, path, cause.Error(), storage.Now(), request, response, processing)
	return err
}
