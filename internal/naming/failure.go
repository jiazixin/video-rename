package naming

import (
	"fmt"
	"strings"

	"video-rename/internal/domain"
)

// DefaultFailureKeywords returns a fresh list so edits cannot change defaults.
func DefaultFailureKeywords() []string {
	return []string{"命名失败", "拒绝", "无法完成", "sorry"}
}

// FailureTitleError checks the complete model title before filename cleaning.
// A nil list uses defaults; an explicitly empty list disables matching.
func FailureTitleError(raw string, keywords []string) *domain.JobError {
	if keywords == nil {
		keywords = DefaultFailureKeywords()
	}
	title := strings.ToLower(raw)
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(title, strings.ToLower(keyword)) {
			return &domain.JobError{
				Code:    domain.CodeNamingFailed,
				Message: fmt.Sprintf("命名失败：模型标题包含失败词句 %q，已保留原文件名", keyword),
			}
		}
	}
	return nil
}
