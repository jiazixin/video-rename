package vision

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/url"
	"strings"
	"unicode/utf8"
)

const omittedDiagnostic = "[response contains sensitive content; omitted]"

// responseDiagnostic extracts only the agreed error message and never logs echoed request or header objects.
// Non-JSON error pages have no verifiable structure, so their bodies are omitted.
func responseDiagnostic(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var envelope struct {
		Error jsontext.Value `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return "[unstructured error response body omitted]"
	}
	var message string
	if json.Unmarshal(envelope.Error, &message) == nil {
		return message
	}
	var detail struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(envelope.Error, &detail) == nil {
		return detail.Message
	}
	return "[error response body omitted]"
}

// safeDiagnostic checks for sensitive content before truncating, so no credential prefix is exposed.
// A match omits the whole message instead of guessing where a secret ends in untrusted text.
func safeDiagnostic(s string, limit int, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		quoted, _ := json.Marshal(secret) // A string always has a JSON representation.
		variants := []string{secret, strings.TrimSpace(secret), url.QueryEscape(secret), url.PathEscape(secret), string(quoted[1 : len(quoted)-1])}
		for _, variant := range variants {
			if variant != "" && strings.Contains(s, variant) {
				return omittedDiagnostic
			}
		}
	}
	lower := strings.ToLower(s)
	for _, marker := range []string{"authorization", "bearer ", "basic ", "api_key", "api-key", "apikey", "data:image", "base64,", "\"prompt\"", "\"messages\"", "\"content\"", "image_url"} {
		if strings.Contains(lower, marker) {
			return omittedDiagnostic
		}
	}
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	const suffix = "…(truncated)"
	end := max(0, limit-len(suffix))
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + suffix
}
