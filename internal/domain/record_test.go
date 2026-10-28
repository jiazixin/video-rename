package domain

import (
	"math"
	"testing"
)

func TestRecordEncodingFailureRemainsVisible(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record Record
	}{
		{"nonfinite detail", Record{Code: "fixture_failure", Details: map[string]any{"value": math.NaN()}}},
		{"unsupported detail", Record{Code: "fixture_failure", Details: map[string]any{"value": make(chan int)}}},
		{"invalid text", Record{Code: "fixture_failure", Message: string([]byte{0xff})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.record.Text()
			got, err := ParseRecord(text)
			if err != nil {
				t.Fatalf("got invalid diagnostic %q: %v, want a decodable record", text, err)
			}
			if got.Code != OutcomeUnexpectedFailure || got.Message == "" || got.Details["record_code"] != tc.record.Code || got.Details["detail"] == "" {
				t.Fatalf("got %+v, want internal encoding failure with the original code and diagnostic", got)
			}
		})
	}
}
