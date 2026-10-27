// Package domain defines the job data, states and error codes shared between modules.
// Module boundaries are described in docs/DESIGN.md:
// media makes no HTTP calls, the model client changes no files, naming performs no renames.
package domain

import (
	"fmt"
	"time"
)

// ErrorCode classifies media, model and file operation errors.
type ErrorCode string

// Error codes, grouped by processing stage.
const (
	// startup and scanning
	CodeToolNotFound ErrorCode = "tool_not_found"

	// probing
	CodeProbeExecFailed      ErrorCode = "probe_exec_failed"
	CodeProbeTimeout         ErrorCode = "probe_timeout"
	CodeProbeOutputLimit     ErrorCode = "probe_output_limit"
	CodeProbeJSONInvalid     ErrorCode = "probe_json_invalid"
	CodeProbeNoVideo         ErrorCode = "probe_no_video"
	CodeProbeDurationUnknown ErrorCode = "probe_duration_unknown"

	// frame extraction
	CodeExtractFailed             ErrorCode = "extract_failed"
	CodeExtractTimeout            ErrorCode = "extract_timeout"
	CodeExtractInsufficientFrames ErrorCode = "extract_insufficient_frames"
	CodeImageTooLarge             ErrorCode = "image_too_large"

	// HTTP
	CodeRequestTooLarge     ErrorCode = "request_too_large"
	CodeAPITimeout          ErrorCode = "api_timeout"
	CodeAPIUnauthorized     ErrorCode = "api_unauthorized"
	CodeAPIRateLimited      ErrorCode = "api_rate_limited"
	CodeAPIHTTPError        ErrorCode = "api_http_error"
	CodeAPIResponseInvalid  ErrorCode = "api_response_invalid"
	CodeAPIResponseTooLarge ErrorCode = "api_response_too_large"

	// naming and commit
	CodeTitleInvalid            ErrorCode = "title_invalid"
	CodeNamingFailed            ErrorCode = "naming_failed"
	CodeModelOutputTruncated    ErrorCode = "model_output_truncated"
	CodeSourceChanged           ErrorCode = "source_changed"
	CodeRenameConflict          ErrorCode = "rename_conflict"
	CodeRenameConflictExhausted ErrorCode = "rename_conflict_exhausted"
	CodeRenameUnsupported       ErrorCode = "rename_unsupported"
	CodeRenamePermissionDenied  ErrorCode = "rename_permission_denied"
	CodeRenameFailed            ErrorCode = "rename_failed"

	// lifecycle
	CodeCancelled ErrorCode = "cancelled"
)

// JobError carries an error code and context from the media process or HTTP exchange.
// Code is the upper-case code clients map to text, Message is English and
// Details carries the values that text needs.
// Global marks a global dependency failure (rejected credentials, unwritable log, vanished external tool)
// that stops scheduling new jobs; ordinary per-video errors do not end the batch.
type JobError struct {
	RetryAfter    time.Duration `json:"-"`
	ProcessingLog string        `json:"-"`
	RawRequest    string        `json:"-"`
	RawResponse   string        `json:"-"`
	Code          ErrorCode
	Message       string
	Details       map[string]any
	ExitCode      int    // media process exit code; 0 when not run or unknown
	HTTPStatus    int    // HTTP status code; 0 for non-HTTP errors
	StderrTail    string // trimmed stderr or redacted response summary
	Global        bool
}

// Record returns the persisted form of e. Exit code, HTTP status and stderr
// tail are added to the details when present.
func (e *JobError) Record() Record {
	details := make(map[string]any, len(e.Details)+3)
	for key, value := range e.Details {
		details[key] = value
	}
	if e.ExitCode != 0 {
		details["exit_code"] = e.ExitCode
	}
	if e.HTTPStatus != 0 {
		details["http_status"] = e.HTTPStatus
	}
	if e.StderrTail != "" {
		details["detail"] = e.StderrTail
	}
	if len(details) == 0 {
		details = nil
	}
	return Record{Code: string(e.Code), Message: e.Error(), Details: details}
}

// Error implements error.
func (e *JobError) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := fmt.Sprintf("%s: %s", e.Code, e.Message)
	if e.ExitCode != 0 {
		msg += fmt.Sprintf(" (exit code %d)", e.ExitCode)
	}
	if e.HTTPStatus != 0 {
		msg += fmt.Sprintf(" (http %d)", e.HTTPStatus)
	}
	if e.StderrTail != "" {
		msg += fmt.Sprintf(" (stderr: %s)", e.StderrTail)
	}
	return msg
}

// Snapshot is a source file identity snapshot: device and inode, size and modification time.
// It deduplicates files and verifies before commit that the source was not modified externally.
type Snapshot struct {
	Dev     uint64
	Ino     uint64
	Size    int64
	ModTime time.Time
}

// Same reports whether both snapshots refer to the same identity with unchanged content.
func (s Snapshot) Same(o Snapshot) bool {
	return s.Dev == o.Dev && s.Ino == o.Ino && s.Size == o.Size && s.ModTime.Equal(o.ModTime)
}

// SameIdentity compares only file system identity (device + inode);
// it decides "unchanged" when a candidate target in the same directory is the source itself (including case-insensitive volumes).
func (s Snapshot) SameIdentity(o Snapshot) bool {
	return s.Dev == o.Dev && s.Ino == o.Ino
}

// VideoJob is a pending video with its normalized absolute path and the identity snapshot from the scan.
type VideoJob struct {
	JobID    string
	Path     string
	Snapshot Snapshot
}

// VideoMeta holds the ffprobe metadata needed by later stages.
type VideoMeta struct {
	StreamIndex   int // absolute input index of the selected video stream
	Duration      time.Duration
	SizeBytes     int64
	Width         int // coded size
	Height        int
	SARNum        int // sample_aspect_ratio numerator; missing or 0:1 means square pixels
	SARDen        int
	Rotation      int // degrees: 0/90/180/270
	DisplayWidth  int // display size after SAR and rotation
	DisplayHeight int
	HDR           bool // HDR transfer characteristic detected; warning only
	HDRNotToneMap bool
}

// FramePlan is a frame extraction plan: time points and review details.
type FramePlan struct {
	Duration    time.Duration   // original duration
	BaseN       int             // base frame count N0 from the rules
	TargetN     int             // final target frame count N
	WindowStart time.Duration   // start A of the effective window
	WindowLen   time.Duration   // length L of the effective window
	Times       []time.Duration // segment midpoints, TargetN entries
}

// Frame is one extracted JPEG frame that passed verification.
type Frame struct {
	Planned      time.Duration // planned time point (midpoint)
	Actual       time.Duration // requested time point (may differ from the plan after a retry)
	Path         string        // absolute path of the temp file
	Width        int
	Height       int
	Bytes        int
	Recompressed bool // re-encoded for exceeding the 1 MiB limit
}

// FailedPoint records a failed sampling point; it ends up in warnings.
type FailedPoint struct {
	Planned time.Duration
	Actual  time.Duration
	Reason  string
}

// FrameSet is the extraction result of one video. TempDir points to a private temp directory
// whose lifecycle belongs to the caller and is managed by the reusable cache.
type FrameSet struct {
	Plan    FramePlan
	Frames  []Frame
	Failed  []FailedPoint
	Warn    []string // stage warnings (degradation, timeouts, cleanup failures)
	TempDir string
}

// TitleResult is a title returned by the model that passed output validation.
type TitleResult struct {
	RawRequest  string `json:"-"`
	RawResponse string `json:"-"`
	Title       string
	Attempts    int // HTTP attempts
	FramesSent  int // frames actually sent (after budget trimming)
}

// RequestBudgets are the built-in resource protection limits.
// They are fixed by the design document and not exposed as configuration.
type RequestBudgets struct {
	ProbeTimeout     time.Duration // ffprobe 30s
	ProbeStdoutLimit int64         // 4 MiB
	ExtractBudget    time.Duration // total extraction budget 120s
	ImageLimit       int64         // 1 MiB per image
	RequestBodyLimit int64         // 16 MiB request body
	ResponseBodyLim  int64         // 1 MiB response body
	StderrTailLimit  int           // 8 KiB of stderr kept
	ResponseSummary  int           // 2 KiB error response summary
}

// DefaultBudgets returns the built-in protection limits defined by the design document.
func DefaultBudgets() RequestBudgets {
	return RequestBudgets{
		ProbeTimeout:     30 * time.Second,
		ProbeStdoutLimit: 4 * 1024 * 1024,
		ExtractBudget:    120 * time.Second,
		ImageLimit:       1024 * 1024,
		RequestBodyLimit: 16 * 1024 * 1024,
		ResponseBodyLim:  1024 * 1024,
		StderrTailLimit:  8 * 1024,
		ResponseSummary:  2 * 1024,
	}
}

// JobBudget computes the total budget of one video:
// 30s probe + 120s extraction + 3 × server.timeout + 30s backoff + 30s wrap-up.
func JobBudget(serverTimeout time.Duration) time.Duration {
	return 30*time.Second + 120*time.Second + 3*serverTimeout + 30*time.Second + 30*time.Second
}
