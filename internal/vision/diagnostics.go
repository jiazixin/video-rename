package vision

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Diagnostics preserve message structure without storing image payloads or credentials.
func diagnosticBody(raw []byte, key string) string    { return diagnosticJSON(raw, key, false) }
func diagnosticRequest(raw []byte, key string) string { return diagnosticJSON(raw, key, true) }
func diagnosticJSON(raw []byte, key string, request bool) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		var estimate map[string]any
		if request {
			estimate = requestEstimate(value, len(raw))
		}
		var clean func(any) any
		clean = func(v any) any {
			switch x := v.(type) {
			case map[string]any:
				for k, item := range x {
					switch strings.ToLower(k) {
					case "authorization", "api_key", "x-api-key", "access_token":
						x[k] = "[REDACTED]"
					case "data":
						if x["type"] == "base64" {
							x[k] = fmt.Sprintf("[image payload omitted: %d characters]", len(fmt.Sprint(item)))
						} else {
							x[k] = clean(item)
						}
					default:
						x[k] = clean(item)
					}
				}
			case []any:
				for i, item := range x {
					x[i] = clean(item)
				}
			case string:
				if strings.HasPrefix(x, "data:image/") {
					return fmt.Sprintf("[image payload omitted: %d characters]", len(x))
				}
			}
			return v
		}
		value = clean(value)
		if obj, ok := value.(map[string]any); ok && request {
			obj["_local_context"] = estimate
		}
		raw, _ = json.Marshal(value, jsontext.WithIndent("  "))
	}
	text := strings.ToValidUTF8(string(raw), "�")
	if key != "" {
		text = strings.ReplaceAll(text, key, "[REDACTED]")
		encoded, _ := json.Marshal(key)
		text = strings.ReplaceAll(text, string(encoded[1:len(encoded)-1]), "[REDACTED]")
	}
	const limit = 128 * 1024
	if len(text) > limit {
		const suffix = "\n[diagnostic truncated at 128 KiB]"
		end := limit - len(suffix)
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end] + suffix
	}
	return text
}

// This deliberately estimates text only: image tokenization is model-specific.
func requestEstimate(value any, bytes int) map[string]any {
	ascii, other, images := 0, 0, 0
	add := func(text string) {
		for _, r := range text {
			if r < utf8.RuneSelf {
				ascii++
			} else {
				other++
			}
		}
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			add(x)
		case []any:
			for _, v := range x {
				walk(v)
			}
		case map[string]any:
			if x["type"] == "image" || x["type"] == "image_url" {
				images++
				return
			}
			if _, ok := x["image_url"]; ok {
				images++
				return
			}
			for _, field := range []string{"text", "content"} {
				if v, ok := x[field]; ok {
					walk(v)
				}
			}
		}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	walk(obj["system"])
	walk(obj["messages"])
	output := obj["max_completion_tokens"]
	if output == nil {
		output = obj["max_tokens"]
	}
	return map[string]any{"text_tokens_estimate": (ascii+3)/4 + (other*3+1)/2, "images": images, "request_bytes": bytes, "max_output_tokens": output, "method": "ASCII characters / 4 + non-ASCII characters × 1.5; excludes image tokens and protocol overhead"}
}
