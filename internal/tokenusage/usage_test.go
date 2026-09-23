package tokenusage

import "testing"

func TestCountPrefersProviderUsageAndFallsBackToSavedEstimate(t *testing.T) {
	request := `{"_local_context":{"text_tokens_estimate":75}}`
	for _, tc := range []struct {
		name, response string
		want           int64
		estimated, ok  bool
	}{
		{"openai total", `{"usage":{"total_tokens":120,"prompt_tokens":90,"completion_tokens":20}}`, 120, false, true},
		{"openai parts", `{"usage":{"prompt_tokens":90,"completion_tokens":20}}`, 110, false, true},
		{"anthropic cached", `{"usage":{"input_tokens":50,"output_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":5}}`, 85, false, true},
		{"fallback", `{"error":"timeout"}`, 75, true, true},
		{"invalid usage", `{"usage":{"total_tokens":-1}}`, 75, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, estimated, ok := Count(request, tc.response)
			if got != tc.want || estimated != tc.estimated || ok != tc.ok {
				t.Fatalf("Count = %d, %v, %v; want %d, %v, %v", got, estimated, ok, tc.want, tc.estimated, tc.ok)
			}
		})
	}
	if _, _, ok := Count(`{"messages":[]}`, `{}`); ok {
		t.Fatal("missing usage and estimate should be unknown")
	}
}
