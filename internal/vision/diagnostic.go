package vision

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"
)

const omittedDiagnostic = "[响应包含敏感内容，已省略]"

// responseDiagnostic 只提取约定的错误消息，不把回显的请求/头部对象写入日志。
// 非 JSON 错误页不具备可验证结构，省略其正文。
func responseDiagnostic(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return "[非结构化错误响应正文已省略]"
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
	return "[错误响应正文已省略]"
}

// safeDiagnostic 在裁剪前检查敏感内容，避免只暴露凭证前缀。
// 命中时省略整段消息，不尝试从不受信任文本中猜测秘密的结束位置。
func safeDiagnostic(s string, limit int, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		quoted, _ := json.Marshal(secret)
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
	const suffix = "…(截断)"
	end := max(0, limit-len(suffix))
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + suffix
}
