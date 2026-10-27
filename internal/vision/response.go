package vision

import (
	"strings"
	"unicode"

	"video-rename/internal/domain"
	"video-rename/internal/naming"
)

// ValidateTitle checks that model output follows the single-line title protocol (docs/processing.md section 4):
//   - only surrounding whitespace and one pair of wrapping quotes are removed;
//   - exactly one non-empty logical line is required;
//   - code fences, lists of titles, explanations and empty titles are title_invalid.
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
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "model output is an empty title", Details: map[string]any{"reason": "empty"}}
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "model output has several lines", Details: map[string]any{"reason": "multiline"}}
	}
	if strings.Contains(s, "```") {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "model output contains a code fence", Details: map[string]any{"reason": "code_fence"}}
	}
	if strings.Contains(s, "\x00") {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "model output contains NUL", Details: map[string]any{"reason": "nul"}}
	}
	// a title made only of control characters or whitespace is invalid
	if strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) == "" {
		return "", &domain.JobError{Code: domain.CodeTitleInvalid, Message: "model output has only blanks or control characters", Details: map[string]any{"reason": "blank"}}
	}
	return s, nil
}

// stripPairedQuotes removes one pair of wrapping quotes (ASCII or CJK).
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
