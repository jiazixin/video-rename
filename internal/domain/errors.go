package domain

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"strings"
)

// Kind classifies an expected failure. The HTTP layer maps it to a status code.
type Kind int

// Failure kinds.
const (
	KindInvalid         Kind = iota + 1 // bad input; 400
	KindNotFound                        // missing record or resource; 404
	KindConflict                        // state does not allow the operation; 409
	KindUnavailable                     // temporarily unavailable; 503
	KindTooMany                         // rate or count limit reached; 429
	KindInternal                        // invariant broken or recovery needed; 500
	KindUnauthenticated                 // missing or expired session; 401
	KindForbidden                       // request not allowed; 403
)

// Error is an expected failure. Code is the snake_case code clients map to
// text, Message is English text for developers and logs, and Details carries
// the values a client needs for its own message. Err is the optional cause;
// its text is passed to clients as details.detail.
type Error struct {
	Kind    Kind
	Code    string
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

// Is reports whether target is an expected failure with the same code, so a
// wrapped copy still matches the failure it was made from.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Fail returns an expected failure. keyValues are alternating detail keys and
// values.
func Fail(kind Kind, code, message string, keyValues ...any) *Error {
	e := &Error{Kind: kind, Code: code, Message: message}
	for i := 0; i+1 < len(keyValues); i += 2 {
		if e.Details == nil {
			e.Details = make(map[string]any, len(keyValues)/2)
		}
		e.Details[fmt.Sprint(keyValues[i])] = keyValues[i+1]
	}
	return e
}

// Wrap returns a copy of e caused by err.
func (e *Error) Wrap(err error) *Error {
	copied := *e
	copied.Err = err
	return &copied
}

// ClientDetails returns the details sent to clients, with the cause's text as
// detail.
func (e *Error) ClientDetails() map[string]any {
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

// Common failure codes.
const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
)

// FieldError is one invalid field of a request or setting. Path names the
// field with dots and list indexes (sampling.size_rules.0.frames); Reason is a
// snake_case reason code.
type FieldError struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Invalid returns the invalid_argument failure for fields.
func Invalid(fields ...FieldError) *Error {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = f.Path + " (" + f.Reason + ")"
	}
	return Fail(KindInvalid, CodeInvalidArgument, "invalid fields: "+strings.Join(parts, ", "), "fields", fields)
}

// InvalidField returns the invalid_argument failure for one field.
func InvalidField(path, reason string) *Error { return Invalid(FieldError{path, reason}) }

// Record is the persisted form of an outcome: the code clients map to text,
// an English message and the details the text needs.
type Record struct {
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// RecordOf converts err for persistence. Errors without a code become
// internal with their text as
// details.detail. Message is the full text of err, including the context of
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
	if e, ok := AsError(err); ok {
		details := e.ClientDetails()
		if _, ok := details["detail"]; !ok && err.Error() != e.Error() {
			details = maps.Clone(details)
			if details == nil {
				details = map[string]any{}
			}
			details["detail"] = err.Error()
		}
		return Record{Code: e.Code, Message: err.Error(), Details: details}
	}
	return Record{Code: CodeInternal, Message: err.Error(), Details: map[string]any{"detail": err.Error()}}
}

// RecordText encodes err for a TEXT column; nil encodes as "".
func RecordText(err error) string { return RecordOf(err).Text() }

// Text encodes r for a TEXT column. The zero Record encodes as "".
func (r Record) Text() string {
	if r.Code == "" && r.Message == "" && len(r.Details) == 0 {
		return ""
	}
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
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
