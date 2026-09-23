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

// Anthropic carries fixed instructions in top-level system, not a user message.
func (c *Client) anthropicRequest(prompt string, frames []domain.Frame) any {
	parts := make([]anthropicPart, 0, 1+len(frames)*2)
	for _, part := range c.contentParts(frames) {
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
	}{c.model, strings.TrimRight(prompt, "\n") + "\n\n" + protocolNote, []chatMessage{{Role: "user", Content: parts}}, c.temperature, c.maxTokens, false}
}
func parseAnthropic(raw []byte, status int) (string, int, *httpJobError) {
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	invalid := func(message string) (string, int, *httpJobError) {
		return "", status, &httpJobError{JobError: domain.JobError{Code: domain.CodeAPIResponseInvalid, Message: message}}
	}
	if json.Unmarshal(raw, &response) != nil {
		return invalid("响应 JSON 格式或字段类型无效")
	}
	if response.StopReason == "max_tokens" {
		return "", status, &httpJobError{JobError: domain.JobError{Code: domain.CodeModelOutputTruncated, Message: "模型输出因长度截断,不作为标题"}}
	}
	if response.StopReason != "end_turn" && response.StopReason != "stop_sequence" {
		return invalid("模型未正常完成文本回答")
	}
	parts := []string{}
	for _, p := range response.Content {
		switch p.Type {
		case "text":
			parts = append(parts, p.Text)
		case "thinking", "redacted_thinking":
			continue
		default:
			return invalid("响应包含非文本标题内容")
		}
	}
	title := strings.Join(parts, "\n")
	if strings.TrimSpace(title) == "" {
		return invalid("模型回答为空")
	}
	return title, status, nil
}
