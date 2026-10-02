package vision

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDiagnosticClippingPreservesUTF8AndTotalBudget(t *testing.T) {
	for _, prefix := range []string{"", "a", "ab"} {
		raw, err := json.Marshal(map[string]string{"content": prefix + strings.Repeat("标题", 30000)})
		if err != nil {
			t.Fatal(err)
		}
		got := diagnosticBody(raw, "")
		if len(got) > 128*1024 || !utf8.ValidString(got) || !strings.Contains(got, "diagnostic truncated") {
			t.Fatalf("invalid clipped diagnostic: bytes=%d valid=%v", len(got), utf8.ValidString(got))
		}
		// The API must be able to encode the captured diagnostic as a string.
		if _, err := json.Marshal(got); err != nil {
			t.Fatal("clipped diagnostic cannot be returned by API", err)
		}
	}
	if got := diagnosticBody([]byte{0xff, 0xfe}, ""); !utf8.ValidString(got) {
		t.Fatal("malformed response bytes cannot be returned as diagnostic")
	}
}

func TestDiagnosticRedaction(t *testing.T) {
	raw := []byte(`{"api_key":"secret","messages":[{"content":[{"image_url":{"url":"data:image/jpeg;base64,AAAA"}},{"source":{"type":"base64","data":"BBBB"}}]}],"echo":"secret","finish_reason":"length","reasoning_content":"still thinking"}`)
	got := diagnosticBody(raw, "secret")
	for _, forbidden := range []string{"secret", "AAAA", "BBBB"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	for _, expected := range []string{"length", "still thinking", "image payload omitted"} {
		if !strings.Contains(got, expected) {
			t.Fatal("missing", expected)
		}
	}
	if len(diagnosticBody([]byte(strings.Repeat("x", 200000)), "")) > 132000 {
		t.Fatal("unbounded")
	}
}

func TestRequestContextEstimateExcludesImagePayload(t *testing.T) {
	for _, raw := range []string{
		`{"messages":[{"role":"system","content":"abcd中文"},{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,AAAA"}}]}],"max_tokens":4096}`,
		`{"system":"abcd中文","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}],"max_tokens":4096}`,
	} {
		var body struct {
			Context struct {
				Text   int `json:"text_tokens_estimate"`
				Images int `json:"images"`
				Bytes  int `json:"request_bytes"`
				Output int `json:"max_output_tokens"`
			} `json:"_local_context"`
		}
		text := diagnosticRequest([]byte(raw), "")
		if err := json.Unmarshal([]byte(text), &body); err != nil {
			t.Fatal(err)
		}
		if body.Context.Text != 4 || body.Context.Images != 1 || body.Context.Bytes != len(raw) || body.Context.Output != 4096 {
			t.Fatalf("%+v", body)
		}
		if strings.Contains(text, "AAAA") {
			t.Fatal("image payload leaked")
		}
	}
	if strings.Contains(diagnosticBody([]byte(`{"usage":{"prompt_tokens":100}}`), ""), "_local_context") {
		t.Fatal("response was annotated as request")
	}
}
