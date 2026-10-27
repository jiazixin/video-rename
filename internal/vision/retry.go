package vision

import (
	"time"

	"video-rename/internal/domain"
)

// httpJobError adds retry control to domain.JobError.
type httpJobError struct {
	domain.JobError
	RetryableFlag bool          // whether the retry policy applies
	RetryAfter    time.Duration // minimum wait required by the server's Retry-After
}

// Retryable reports whether the error enters the bounded retry loop.
// Cancellation, rejected credentials, protocol errors and exceeded limits are never retried.
func (e *httpJobError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case domain.CodeCancelled, domain.CodeAPIUnauthorized,
		domain.CodeAPIResponseInvalid, domain.CodeAPIResponseTooLarge,
		domain.CodeModelOutputTruncated, domain.CodeTitleInvalid, domain.CodeNamingFailed,
		domain.CodeRequestTooLarge:
		return false
	}
	if e.RetryableFlag {
		return true
	}
	// transport errors (timeouts, connection resets, EOF) are retryable by default in the Title loop
	return e.HTTPStatus == 0 && e.Code != domain.CodeAPIUnauthorized
}
