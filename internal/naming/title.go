// Package naming 实现标题失败词句检查、清洗与同目录候选名称分配。
// 清洗规则固定(docs/operations.md 第 2 节),不含策略开关;
// 命名模块不执行 rename。
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

// 文件名硬上限。
const MaxFilenameBytes = 240 // 完整文件名字节上限

// NormalizeExtension 统一扩展名小写，并将 JPEG 使用的后缀规范为 .jpg。
func NormalizeExtension(ext string) string {
	ext = strings.ToLower(ext)
	if ext == ".jpeg" {
		return ".jpg"
	}
	return ext
}

// windowsReserved Windows 保留名称(含后接点的形式)。
var windowsReserved = buildReserved()

func buildReserved() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("COM%d", i)] = true
		m[fmt.Sprintf("LPT%d", i)] = true
	}
	return m
}

// specialChars 文件名中替换为下划线的字符。
const specialChars = `/\:*?"<>|`

// CleanTitle 按固定顺序清洗模型标题(不含扩展名):
//  1. NFC 规范化;删除 NUL、控制字符和双向格式控制字符(Cc/Cf);
//  2. 将 / \ : * ? " < > | 替换为 _,合并连续空格与重复 _;
//  3. 去除首尾空白和句点(删除导致隐藏文件的起始句点);
//  4. 末尾与扩展名相同的后缀(忽略大小写)去除一次;
//  5. Windows 保留名称加 _ 前缀;
//
// 不限制标题码点数;完整文件名的字节预算由 Allocator 按候选统一预留。
//
// 结果为空、"." 或 ".." 时返回 TITLE_INVALID,不以随机名替代。
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
	// 去除与扩展名相同的尾部后缀一次,避免 .mp4.mp4
	if ext != "" && len(s) > len(ext) {
		tail := s[len(s)-len(ext):]
		if strings.EqualFold(tail, ext) {
			s = strings.TrimRight(s[:len(s)-len(ext)], " \t.。")
		}
	}
	// Windows 保留名称
	upper := strings.ToUpper(s)
	if windowsReserved[upper] || hasReservedPrefix(upper) {
		s = "_" + s
	}
	if s == "" || s == "." || s == ".." {
		return "", &domain.JobError{
			Code:    domain.CodeTitleInvalid,
			Message: fmt.Sprintf("清洗后的标题无效(%q)", raw),
		}
	}
	return s, nil
}

// hasReservedPrefix 判断 "CON.xxx" 形式。
func hasReservedPrefix(upper string) bool {
	for name := range maps.Keys(windowsReserved) {
		if strings.HasPrefix(upper, name+".") {
			return true
		}
	}
	return false
}

// collapseRuns 合并连续空白为单个空格、连续下划线为单个下划线。
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

// TruncateToBytes 在字节预算内按 rune 截断(不切断 UTF-8 编码),
// 截断后去掉尾部空白和句点;预算过小导致空串时返回错误。
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
			Message: "字节预算内无法容纳有效标题",
		}
	}
	return s, nil
}
