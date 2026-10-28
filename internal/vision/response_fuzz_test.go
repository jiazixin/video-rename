package vision

import (
	"net/http"
	"strings"
	"testing"
)

func FuzzModelResponse(f *testing.F) {
	for _, input := range []string{`{}`, `{"choices":[{"message":{"content":"fixture title"},"finish_reason":"stop"}]}`, `{"content":[{"type":"text","text":"title"}],"stop_reason":"end_turn"}`, `{"content":null}`, `{"content":[{"type":"tool_use"}],"stop_reason":"end_turn"}`} {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		title, _, err := parseAnthropic(input, http.StatusOK)
		if err == nil && strings.TrimSpace(title) == "" {
			t.Fatal("got empty successful title, want text")
		}
		if title, _, err := parseOpenAI(input, http.StatusOK, nil); err == nil && strings.TrimSpace(title) == "" {
			t.Fatal("got empty OpenAI title, want text")
		}
		// Diagnostic rendering must remain bounded for arbitrary server data.
		if got := diagnosticBody(input, "fixture-secret"); strings.Contains(got, "fixture-secret") {
			t.Fatal("got unredacted diagnostic, want secret removed")
		}
	})
}
