// Package naming checks titles for failure phrases, cleans them and allocates candidate names within a directory.
// The cleaning rules are fixed (docs/operations.md section 2) with no policy switches;
// naming performs no renames.
package naming

import (
	"fmt"
	"maps"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"video-rename/internal/domain"
)

// MaxFilenameBytes is the hard limit on the length of a complete file name.
const MaxFilenameBytes = 240

// NormalizeExtension lowercases the extension and normalizes JPEG suffixes to .jpg.
func NormalizeExtension(ext string) string {
	ext = strings.ToLower(ext)
	if ext == ".jpeg" {
		return ".jpg"
	}
	return ext
}

// windowsReserved lists Windows reserved names (including forms followed by a dot).
var windowsReserved = buildReserved()

func buildReserved() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("COM%d", i)] = true
		m[fmt.Sprintf("LPT%d", i)] = true
	}
	return m
}

// specialChars are file name characters replaced with an underscore.
const specialChars = `/\:*?"<>|`

// CleanTitle cleans a model title (without extension) in a fixed order:
//  1. NFC normalization; remove NUL, control and bidirectional format characters (Cc/Cf);
//  2. replace / \ : * ? " < > | with _, collapse repeated spaces and underscores;
//  3. trim surrounding whitespace and periods (removing a leading period that would hide the file);
//  4. remove one trailing suffix equal to the extension (case-insensitive);
//  5. prefix Windows reserved names with _;
//
// Title code points are not limited; Allocator reserves the byte budget of the complete file name per candidate.
//
// An empty, "." or ".." result returns title_invalid instead of a random name.
func CleanTitle(raw, ext string) (string, *domain.JobError) {
	s := norm.NFC.String(raw)
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(specialChars, r) {
			return '_'
		}
		return r
	}, s)
	s = collapseRuns(s)
	s = strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.' || r == '。'
	})
	// remove a trailing suffix equal to the extension once, avoiding .mp4.mp4
	if ext != "" && len(s) > len(ext) {
		tail := s[len(s)-len(ext):]
		if strings.EqualFold(tail, ext) {
			s = strings.TrimRight(s[:len(s)-len(ext)], " \t.。")
		}
	}
	// Windows reserved names
	upper := strings.ToUpper(s)
	if windowsReserved[upper] || hasReservedPrefix(upper) {
		s = "_" + s
	}
	if s == "" || s == "." || s == ".." {
		return "", &domain.JobError{
			Code:    domain.CodeTitleInvalid,
			Message: fmt.Sprintf("title is invalid after cleaning (%q)", raw),
			Details: map[string]any{"title": raw},
		}
	}
	return s, nil
}

// hasReservedPrefix detects the "CON.xxx" form.
func hasReservedPrefix(upper string) bool {
	for name := range maps.Keys(windowsReserved) {
		if strings.HasPrefix(upper, name+".") {
			return true
		}
	}
	return false
}

// collapseRuns collapses runs of whitespace into one space and runs of underscores into one underscore.
func collapseRuns(s string) string {
	var b strings.Builder
	var prev rune
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			if prev != ' ' {
				b.WriteRune(' ')
				prev = ' '
			}
		case r == '_':
			if prev != '_' {
				b.WriteRune('_')
				prev = '_'
			}
		default:
			b.WriteRune(r)
			prev = r
		}
	}
	return b.String()
}

// TruncateToBytes truncates by rune within the byte budget (never splitting UTF-8 sequences),
// then trims trailing whitespace and periods; it returns an error when the budget leaves an empty string.
func TruncateToBytes(s string, maxBytes int, ext string) (string, *domain.JobError) {
	if len(s) > maxBytes {
		// Inspect only the cut boundary; repeatedly rebuilding a long model
		// title for each removed rune would make truncation quadratic.
		end := max(0, maxBytes)
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end]
	}
	s = strings.TrimRight(s, " \t.。")
	if s == "" || s == "." || s == ".." {
		return "", &domain.JobError{
			Code:    domain.CodeTitleInvalid,
			Message: "no valid title fits the byte budget",
			Details: map[string]any{"reason": "byte_budget"},
		}
	}
	return s, nil
}
