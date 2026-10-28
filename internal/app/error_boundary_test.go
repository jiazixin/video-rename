package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestValidationErrorHTTPBoundary(t *testing.T) {
	validation := domain.Invalid(domain.FieldError{Path: "items.0.name", Reason: "required"}, domain.FieldError{Path: "limit", Reason: "out_of_range"}).Wrap(errors.New("private database diagnostic"))
	recorder := httptest.NewRecorder()
	writeError(recorder, httptest.NewRequest(http.MethodPost, "/api/test", nil), fmt.Errorf("save item: %w", validation))
	var result struct {
		Error errorBody `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusBadRequest || result.Error.Code != "invalid_argument" || strings.Contains(recorder.Body.String(), "private database") {
		t.Fatalf("got %d %s, want typed validation without internal cause", recorder.Code, recorder.Body.String())
	}
	var fields struct {
		Fields []fieldIssue `json:"fields"`
	}
	raw, err := json.Marshal(result.Error.Details)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields.Fields) != 2 || fields.Fields[0].Path != "items.0.name" || fields.Fields[1].Reason != "out_of_range" {
		t.Fatalf("got %+v, want both field paths and reasons", fields)
	}
}

func TestUnknownDomainReasonDoesNotBecomeWireCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, httptest.NewRequest(http.MethodGet, "/api/test", nil), domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.FailureReason(-1), Message: "private invariant"}))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "private invariant") {
		t.Fatalf("got %d %s, want generic internal error", recorder.Code, recorder.Body.String())
	}
}
