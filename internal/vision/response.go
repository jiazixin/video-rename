package vision

import (
	"strings"
	"unicode"

	"video-rename/internal/domain"
)

// ValidateTitle 验证模型输出符合单行标题协议(docs/processing.md 第 4 节):
//   - 只移除首尾空白及成对包裹引号;
//   - 要求仅一个非空逻辑行;
//   - 代码围栏、多标题列表、解释段落、空标题均为 TITLE_INVALID。
func ValidateTitle(raw string) (string, *domain.JobError) {
	s := strings.TrimSpace(raw)
	s = stripPairedQuotes(s)
	// The reserved failure marker must never reach filename cleaning or commit.
	if strings.Contains(s, "命名失败") {
		return "", &domain.JobError{Code: domain.CodeNamingFailed, Message: "命名失败：模型表示无法生成符合要求的标题，已保留原文件名"}
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
