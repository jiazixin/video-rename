package vision

import (
	"encoding/json/v2"
	"strings"

	"video-rename/internal/domain"
)

type anthropicPart struct {
	Type   string           `json:"type"`
	Text   string           `json:"text,omitempty"`
	Source *anthropicSource `json:"source,omitempty"`
}
type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// Anthropic carries the whole prompt in top-level system, not a user message.
func (c *Client) anthropicRequest(prompt string, content []contentPart) any {
	parts := make([]anthropicPart, 0, len(content))
	for _, part := range content {
		if part.Type == "text" {
			parts = append(parts, anthropicPart{Type: "text", Text: part.Text})
		} else {
			parts = append(parts, anthropicPart{Type: "image", Source: &anthropicSource{Type: "base64", MediaType: "image/jpeg", Data: strings.TrimPrefix(part.ImageURL.URL, "data:image/jpeg;base64,")}})
		}
	}
	return struct {
		Model       string        `json:"model"`
		System      string        `json:"system"`
		Messages    []chatMessage `json:"messages"`
		Temperature float64       `json:"temperature"`
		MaxTokens   int           `json:"max_tokens"`
		Stream      bool          `json:"stream"`
	}{c.model, strings.TrimRight(prompt, "\n"), []chatMessage{{Role: "user", Content: parts}}, c.temperature, c.maxTokens, false}
}
func parseAnthropic(raw []byte, status int) (string, int, *httpJobError) {
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	invalid := func(reason, message string) (string, int, *httpJobError) {
		return "", status, &httpJobError{Code: domain.CodeAPIResponseInvalid, Message: message, Details: map[string]any{"reason": reason}}
	}
	if json.Unmarshal(raw, &response) != nil {
		return invalid("json", "response JSON or field types are invalid")
	}
	if response.StopReason == "max_tokens" {
		return "", status, &httpJobError{Code: domain.CodeModelOutputTruncated, Message: "model output was truncated; not used as a title"}
	}
	if response.StopReason != "end_turn" && response.StopReason != "stop_sequence" {
		return invalid("not_completed", "model did not complete a text answer")
	}
	parts := []string{}
	for _, p := range response.Content {
		switch p.Type {
		case "text":
			parts = append(parts, p.Text)
		case "thinking", "redacted_thinking":
			continue
		default:
			return invalid("non_text", "response contains non-text title content")
		}
	}
	title := strings.Join(parts, "\n")
	if strings.TrimSpace(title) == "" {
		return invalid("empty_answer", "model answer is empty")
	}
	return title, status, nil
}
