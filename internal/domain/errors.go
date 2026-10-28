package domain

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"strings"
)

// Kind classifies expected failures without depending on a transport.
type Kind int

// Failure kinds.
const (
	KindInvalid         Kind = iota + 1 // bad input
	KindNotFound                        // missing record or resource
	KindConflict                        // state does not allow the operation
	KindUnavailable                     // temporarily unavailable
	KindTooMany                         // rate or count limit reached
	KindInternal                        // invariant broken or recovery needed
	KindUnauthenticated                 // missing or expired session
	KindForbidden                       // request not allowed
)

// Error is a typed domain failure with an optional underlying cause.
type Error struct {
	Kind    Kind
	Reason  FailureReason
	Message string
	Details map[string]any
	Err     error
}

// Error implements error.
func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is an expected failure with the same reason, so a
// wrapped copy still matches the failure it was made from.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Reason == e.Reason
}

// Fail returns an expected failure with structured diagnostic details.
func Fail(kind Kind, failure Failure) *Error {
	return &Error{Kind: kind, Reason: failure.Reason, Message: failure.Message, Details: failure.Details}
}

// Wrap returns a copy of e caused by err.
func (e *Error) Wrap(err error) *Error {
	copied := *e
	copied.Err = err
	return &copied
}

// DiagnosticDetails includes the cause for persisted diagnostics, not HTTP responses.
func (e *Error) DiagnosticDetails() map[string]any {
	if e.Err == nil {
		return e.Details
	}
	details := make(map[string]any, len(e.Details)+1)
	for key, value := range e.Details {
		details[key] = value
	}
	if _, ok := details["detail"]; !ok {
		details["detail"] = e.Err.Error()
	}
	return details
}

// AsError returns the expected failure in the chain of err.
func AsError(err error) (*Error, bool) { return errors.AsType[*Error](err) }

// OutcomeUnexpectedFailure identifies a diagnostic that has no expected domain reason.
const OutcomeUnexpectedFailure = "unexpected_failure"

// FieldError identifies one invalid domain field and its validation reason.
type FieldError struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ValidationError reports invalid fields without an HTTP code or status.
type ValidationError struct {
	Fields []FieldError
	Err    error
}

// Error describes the invalid fields for developers.
func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, field := range e.Fields {
		parts[i] = field.Path + " (" + field.Reason + ")"
	}
	return "invalid fields: " + strings.Join(parts, ", ")
}

// Unwrap returns the validation cause, if present.
func (e *ValidationError) Unwrap() error { return e.Err }

// Wrap attaches a cause to a copy of the validation failure.
func (e *ValidationError) Wrap(err error) *ValidationError {
	copied := *e
	copied.Err = err
	return &copied
}

// Invalid describes the invalid fields.
func Invalid(fields ...FieldError) *ValidationError { return &ValidationError{Fields: fields} }

// InvalidField describes one invalid field.
func InvalidField(path, reason string) *ValidationError {
	return Invalid(FieldError{Path: path, Reason: reason})
}

// Record is the persisted form of an outcome: the code clients map to text,
// an English message and the details the text needs.
type Record struct {
	Code    string         `json:"code,omitzero"`
	Message string         `json:"message,omitzero"`
	Details map[string]any `json:"details,omitzero"`
}

// IsZero reports whether the record contains no outcome or diagnostic.
func (r Record) IsZero() bool { return r.Code == "" && r.Message == "" && len(r.Details) == 0 }

// RecordOf converts err for persistence. Errors without a code become
// unexpected_failure with their text in details.detail. Message is the full text of err, including the context of
// wrapping errors; that context also becomes details.detail when the
// failure has no detail of its own.
func RecordOf(err error) Record {
	if err == nil {
		return Record{}
	}
	if je, ok := errors.AsType[*JobError](err); ok {
		r := je.Record()
		r.Message = err.Error()
		return r
	}
	if e, ok := errors.AsType[*ValidationError](err); ok {
		return Record{Code: "validation_failed", Message: err.Error(), Details: map[string]any{"fields": e.Fields}}
	}
	if e, ok := AsError(err); ok {
		details := e.DiagnosticDetails()
		if _, ok := details["detail"]; !ok && err.Error() != e.Error() {
			details = maps.Clone(details)
			if details == nil {
				details = map[string]any{}
			}
			details["detail"] = err.Error()
		}
		return Record{Code: outcomeCodes[e.Reason], Message: err.Error(), Details: details}
	}
	return Record{Code: OutcomeUnexpectedFailure, Message: err.Error(), Details: map[string]any{"detail": err.Error()}}
}

// RecordText encodes err for a TEXT column; nil encodes as "".
func RecordText(err error) string { return RecordOf(err).Text() }

// Text encodes r for a TEXT column. The zero Record encodes as "".
func (r Record) Text() string {
	if r.IsZero() {
		return ""
	}
	b, err := json.Marshal(r)
	if err == nil {
		return string(b)
	}
	// Serialization must not turn a failure into the empty, successful outcome.
	// The diagnostic contains only strings; invalid external text is replaced
	// by the encoder so even a malformed original code remains reportable.
	diagnostic, diagnosticErr := json.Marshal(Record{
		Code:    OutcomeUnexpectedFailure,
		Message: "outcome record encoding failed",
		Details: map[string]any{"record_code": r.Code, "detail": err.Error()},
	}, jsontext.AllowInvalidUTF8(true))
	if diagnosticErr != nil {
		panic(fmt.Errorf("encode string-only record diagnostic: %w", diagnosticErr))
	}
	return string(diagnostic)
}

// ParseRecord decodes column text written by Text.
func ParseRecord(text string) (Record, error) {
	var r Record
	if text == "" {
		return r, nil
	}
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return Record{}, fmt.Errorf("record: %w", err)
	}
	return r, nil
}

// Scan implements sql.Scanner for TEXT columns written by Text.
func (r *Record) Scan(src any) error {
	var err error
	switch v := src.(type) {
	case nil:
		*r = Record{}
	case string:
		*r, err = ParseRecord(v)
	case []byte:
		*r, err = ParseRecord(string(v))
	default:
		return fmt.Errorf("record: unsupported column type %T", src)
	}
	return err
}
