package app

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"

	"video-rename/internal/domain"
)

// send writes v as the JSON response. A slice is sent as the list object
// {"items": [...]}, never as a bare array (代码规范.md §5.2).
func send(w http.ResponseWriter, status int, v any) {
	if kind := reflect.ValueOf(v).Kind(); kind == reflect.Slice || kind == reflect.Array {
		v = map[string]any{"items": v}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v)
}

// statusOf maps failure kinds to HTTP status codes.
var statusOf = map[domain.Kind]int{
	domain.KindInvalid:         http.StatusBadRequest,
	domain.KindNotFound:        http.StatusNotFound,
	domain.KindConflict:        http.StatusConflict,
	domain.KindTooMany:         http.StatusTooManyRequests,
	domain.KindUnavailable:     http.StatusServiceUnavailable,
	domain.KindInternal:        http.StatusInternalServerError,
	domain.KindUnauthenticated: http.StatusUnauthorized,
	domain.KindForbidden:       http.StatusForbidden,
}

// errorBody is the error member of the response in docs/API.md.
type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitzero"`
}

// Failures of the HTTP layer itself.
var (
	errShuttingDown     = domain.Fail(domain.KindUnavailable, "shutting_down", "service is shutting down")
	errNotFound         = domain.Fail(domain.KindNotFound, domain.CodeNotFound, "no such API endpoint")
	errMethodNotAllowed = &domain.Error{Kind: domain.KindConflict, Code: "method_not_allowed", Message: "method not allowed"}
	errOriginRejected   = domain.Fail(domain.KindForbidden, "permission_denied", "cross-site request rejected")
	errNotJSON          = &domain.Error{Kind: domain.KindInvalid, Code: "unsupported_media_type", Message: "request body must be application/json"}
	errTooLarge         = &domain.Error{Kind: domain.KindInvalid, Code: "payload_too_large", Message: "request body too large"}
)

// writeError writes err as the error response of docs/API.md. Expected
// failures carry their own code and details; job errors use their code; any
// other error is logged with the request ID and answered with 500 internal
// without its text.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, body := errorResponse(r, err)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	send(w, status, map[string]any{"error": body})
}

// errorResponse returns the status and error member of err for writeError.
func errorResponse(r *http.Request, err error) (int, errorBody) {
	switch {
	case errors.Is(err, errMethodNotAllowed):
		return http.StatusMethodNotAllowed, errorBody{Code: errMethodNotAllowed.Code, Message: errMethodNotAllowed.Message}
	case errors.Is(err, errNotJSON):
		return http.StatusUnsupportedMediaType, errorBody{Code: errNotJSON.Code, Message: errNotJSON.Message}
	case errors.Is(err, errTooLarge):
		return http.StatusRequestEntityTooLarge, errorBody{Code: errTooLarge.Code, Message: errTooLarge.Message}
	}
	if e, ok := domain.AsError(err); ok {
		status := statusOf[e.Kind]
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return status, errorBody{Code: e.Code, Message: e.Error(), Details: e.ClientDetails()}
	}
	if je, ok := errors.AsType[*domain.JobError](err); ok {
		record := je.Record()
		return http.StatusConflict, errorBody{Code: record.Code, Message: err.Error(), Details: record.Details}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusNotFound, errorBody{Code: domain.CodeNotFound, Message: "record not found"}
	}
	requestLogger(r).Error("request_failed", "method", r.Method, "path", r.URL.Path, "error", err)
	return http.StatusInternalServerError, errorBody{Code: domain.CodeInternal, Message: "internal error"}
}

// reject writes an expected failure; keyValues are detail keys and values.
func reject(w http.ResponseWriter, r *http.Request, kind domain.Kind, code, message string, keyValues ...any) {
	writeError(w, r, domain.Fail(kind, code, message, keyValues...))
}

// invalid writes the invalid_argument failure of one request field.
func invalid(w http.ResponseWriter, r *http.Request, path, reason string) {
	writeError(w, r, domain.InvalidField(path, reason))
}

// decode reads one JSON object of at most 64 KiB, rejecting other media types,
// unknown members and trailing data.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, errNotJSON)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	d := jsontext.NewDecoder(r.Body)
	if d.PeekKind() != '{' {
		// PeekKind hides read errors; reading the token reports them.
		_, readErr := d.ReadToken()
		if _, tooLarge := errors.AsType[*http.MaxBytesError](readErr); tooLarge {
			writeError(w, r, errTooLarge)
			return false
		}
		invalid(w, r, "", "not_object")
		return false
	}
	if err := json.UnmarshalDecode(d, v, json.RejectUnknownMembers(true)); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			writeError(w, r, errTooLarge)
			return false
		}
		invalid(w, r, "", "invalid_json")
		return false
	}
	if _, err := d.ReadValue(); !errors.Is(err, io.EOF) {
		invalid(w, r, "", "trailing_data")
		return false
	}
	return true
}
func number(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

// creationOrUpdateID keeps malformed PATCH paths from becoming creates (ID 0).
func creationOrUpdateID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if r.Method == http.MethodPost {
		return 0, true
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		invalid(w, r, "id", "invalid")
		return 0, false
	}
	return id, true
}
