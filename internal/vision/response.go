package vision

import (
	"strings"
	"unicode"

	"video-rename/internal/domain"
	"video-rename/internal/naming"
)

// ValidateTitle 验证模型输出符合单行标题协议(docs/processing.md 第 4 节):
//   - 只移除首尾空白及成对包裹引号;
//   - 要求仅一个非空逻辑行;
//   - 代码围栏、多标题列表、解释段落、空标题均为 TITLE_INVALID。
//
// The run's failure rules are applied first. Empty rules disable matching,
// while nil uses defaults.
func ValidateTitle(raw string, keywords []string) (string, *domain.JobError) {
	s := strings.TrimSpace(raw)
	s = stripPairedQuotes(s)
	if err := naming.FailureTitleError(s, keywords); err != nil {
		return "", err
	}
	if s == "" {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "模型输出为空标题"}
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "模型输出包含多行内容"}
	}
	if strings.Contains(s, "```") {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "模型输出包含代码围栏"}
	}
	if strings.Contains(s, "\x00") {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "模型输出包含 NUL"}
	}
	// 全部由控制字符或空白组成视为无效
	if strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) == "" {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "模型输出仅含空白或控制字符"}
	}
	return s, nil
}

// stripPairedQuotes 移除一层成对包裹引号(含中英文引号)。
func stripPairedQuotes(s string) string {
	for {
		runes := []rune(strings.TrimSpace(s))
		if len(runes) < 2 {
			return string(runes)
		}
		first, last := runes[0], runes[len(runes)-1]
		paired := (first == '"' && last == '"') ||
			(first == '\'' && last == '\'') ||
			(first == '“' && last == '”') ||
			(first == '「' && last == '」') ||
			(first == '『' && last == '』')
		if !paired {
			return string(runes)
		}
		s = string(runes[1 : len(runes)-1])
	}
}
