package app

import (
	"bytes"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"

	"video-rename/internal/auth"
	"video-rename/internal/domain"
)

// send writes v as the JSON response. A slice is sent as the list object
// {"items": [...]}, never as a bare array.
func send(w http.ResponseWriter, status int, v any) {
	if kind := reflect.ValueOf(v).Kind(); kind == reflect.Slice || kind == reflect.Array {
		v = map[string]any{"items": v}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("encode response: %w", err))
	}
	raw, tag, err := annotateETags(raw)
	if err != nil {
		panic(fmt.Errorf("encode response tags: %w", err))
	}
	if tag != "" {
		w.Header().Set("ETag", tag)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The response is already committed. A disconnected client cannot receive a
	// second error response; the HTTP server owns connection cleanup.
	_, _ = w.Write(raw)
}

// annotateETags preserves raw number tokens while adding resource validators.
func annotateETags(raw jsontext.Value) (jsontext.Value, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, "", nil
	}
	switch trimmed[0] {
	case '{':
		var object map[string]jsontext.Value
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, "", err
		}
		var tag string
		var version int64
		if value, exists := object["version"]; exists && json.Unmarshal(value, &version) == nil && version > 0 {
			tag = entityTag(version)
			encoded, err := json.Marshal(tag)
			if err != nil {
				return nil, "", err
			}
			object["etag"] = encoded
		}
		for name, value := range object {
			annotated, _, err := annotateETags(value)
			if err != nil {
				return nil, "", err
			}
			object[name] = annotated
		}
		encoded, err := json.Marshal(object)
		return encoded, tag, err
	case '[':
		var items []jsontext.Value
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, "", err
		}
		for index, value := range items {
			annotated, _, err := annotateETags(value)
			if err != nil {
				return nil, "", err
			}
			items[index] = annotated
		}
		encoded, err := json.Marshal(items)
		return encoded, "", err
	default:
		return raw, "", nil
	}
}

// statusOf maps failure kinds to HTTP status codes.
var statusOf = map[domain.Kind]int{
	domain.KindInvalid:         http.StatusBadRequest,
	domain.KindNotFound:        http.StatusNotFound,
	domain.KindConflict:        http.StatusConflict,
	domain.KindTooMany:         http.StatusConflict,
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

// httpFailure is owned by the transport, including its wire code and status.
type httpFailure struct {
	status int
	body   errorBody
}

func (e *httpFailure) Error() string { return e.body.Message }

var (
	errShuttingDown     = &httpFailure{http.StatusServiceUnavailable, errorBody{Code: "shutting_down", Message: "service is shutting down"}}
	errNotFound         = &httpFailure{http.StatusNotFound, errorBody{Code: "not_found", Message: "no such API endpoint"}}
	errMethodNotAllowed = &httpFailure{http.StatusMethodNotAllowed, errorBody{Code: "method_not_allowed", Message: "method not allowed"}}
	errOriginRejected   = &httpFailure{http.StatusForbidden, errorBody{Code: "permission_denied", Message: "cross-site request rejected"}}
	errNotJSON          = &httpFailure{http.StatusUnsupportedMediaType, errorBody{Code: "unsupported_media_type", Message: "request body must be application/json"}}
	errTooLarge         = &httpFailure{http.StatusRequestEntityTooLarge, errorBody{Code: "payload_too_large", Message: "request body too large"}}
)

// writeError maps typed failures to the wire contract of docs/API.md. Unknown
// failures are logged with the request ID and answered with 500 internal
// without exposing their text.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, body := errorResponse(r, err)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	send(w, status, map[string]any{"error": body})
}

// errorResponse returns the status and error member of err for writeError.
func errorResponse(r *http.Request, err error) (int, errorBody) {
	if failure, ok := errors.AsType[*httpFailure](err); ok {
		return failure.status, failure.body
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		return http.StatusUnauthorized, errorBody{Code: "unauthenticated", Message: "login required"}
	}
	if errors.Is(err, auth.ErrRateLimited) {
		return http.StatusTooManyRequests, errorBody{Code: "rate_limited", Message: "too many login attempts"}
	}
	if validation, ok := errors.AsType[*domain.ValidationError](err); ok {
		fields := make([]fieldIssue, len(validation.Fields))
		for i, field := range validation.Fields {
			fields[i] = fieldIssue{Path: field.Path, Reason: field.Reason}
		}
		return http.StatusBadRequest, errorBody{Code: "invalid_argument", Message: "invalid request fields", Details: map[string]any{"fields": fields}}
	}
	if failure, ok := domain.AsError(err); ok {
		if code, known := failureCodes[failure.Reason]; known {
			switch failure.Reason {
			case domain.ReasonPromptChanged, domain.ReasonModelConfigChanged, domain.ReasonScheduleChanged, domain.ReasonEntryChanged:
				return http.StatusPreconditionFailed, errPrecondition.body
			}
			if status := statusOf[failure.Kind]; status != 0 {
				return status, errorBody{Code: code, Message: failure.Message, Details: failure.Details}
			}
		}
	}
	if je, ok := errors.AsType[*domain.JobError](err); ok {
		record := je.Record()
		if code, known := jobFailureCodes[je.Code]; known {
			return http.StatusConflict, errorBody{Code: code, Message: err.Error(), Details: record.Details}
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusNotFound, errorBody{Code: "not_found", Message: "record not found"}
	}
	requestLogger(r).Error("request_failed", "method", r.Method, "path", r.URL.Path, "error", err)
	return http.StatusInternalServerError, errorBody{Code: "internal", Message: "internal error"}
}

// decode reads one JSON object of at most 64 KiB, rejecting other media types,
// unknown members and trailing data.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" && !(r.Method == http.MethodPatch && mediaType == "application/merge-patch+json")) {
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
		writeError(w, r, domain.InvalidField("", "not_object"))
		return false
	}
	if err := json.UnmarshalDecode(d, v, json.RejectUnknownMembers(true)); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			writeError(w, r, errTooLarge)
			return false
		}
		writeError(w, r, domain.InvalidField("", "invalid_json"))
		return false
	}
	if _, err := d.ReadValue(); !errors.Is(err, io.EOF) {
		writeError(w, r, domain.InvalidField("", "trailing_data"))
		return false
	}
	return true
}

// number reads an integer already checked by router.HandleFunc or
// requireSession. An omitted optional query value stays zero for normalization.
func number(value string) int64 {
	if value == "" {
		return 0
	}
	// Both HTTP boundaries reject parse failures before invoking a handler.
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

// creationOrUpdateID keeps malformed PATCH paths from becoming creates (ID 0).
func creationOrUpdateID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if r.Method == http.MethodPost {
		return 0, true
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, r, domain.InvalidField("id", "invalid"))
		return 0, false
	}
	return id, true
}

// fieldIssue is the HTTP representation of one domain validation issue.
type fieldIssue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
