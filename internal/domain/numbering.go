package domain

import (
	"fmt"
	"strings"
)

// MediaNumberingRules shapes how one media class is numbered. The zero value
// is not usable; always start from DefaultMediaNumberingRules.
type MediaNumberingRules struct {
	Padding   int    `json:"padding"`   // minimum digits, numbers are zero-padded up to it
	Lowercase bool   `json:"lowercase"` // normalize the extension to lower case
	Prefix    string `json:"prefix"`    // only prefix+digits names count as numbered
	Compact   bool   `json:"compact"`   // renumber from 1, ignoring gaps
}

// NumberingSettings is the persisted rule set shared by both media classes.
type NumberingSettings struct {
	Image  MediaNumberingRules `json:"image"`
	Audio  MediaNumberingRules `json:"audio"`
	Shared bool                `json:"shared"` // one sequence shared by images and audio per directory
}

func DefaultMediaNumberingRules() MediaNumberingRules {
	return MediaNumberingRules{Padding: 1, Lowercase: true, Compact: true}
}
func DefaultNumberingSettings() NumberingSettings {
	return NumberingSettings{Image: DefaultMediaNumberingRules(), Audio: DefaultMediaNumberingRules()}
}

// ValidateNumberingSettings rejects values the numbering engine cannot apply
// safely; generated names are still validated per item at preview time.
func ValidateNumberingSettings(s NumberingSettings) error {
	for _, class := range []struct {
		name  string
		rules MediaNumberingRules
	}{
		{"图片", s.Image}, {"音频", s.Audio},
	} {
		if class.rules.Padding < 1 || class.rules.Padding > 9 {
			return fmt.Errorf("%s编号位数须在 1–9 之间", class.name)
		}
		if len([]rune(class.rules.Prefix)) > 32 {
			return fmt.Errorf("%s编号前缀最长 32 个字符", class.name)
		}
		if strings.ContainsAny(class.rules.Prefix, "/\\\x00") || strings.ContainsFunc(class.rules.Prefix, func(c rune) bool { return c < 0x20 }) {
			return fmt.Errorf("%s编号前缀不能包含路径分隔符或控制字符", class.name)
		}
		if strings.TrimSpace(class.rules.Prefix) != class.rules.Prefix {
			return fmt.Errorf("%s编号前缀不能以空白开头或结尾", class.name)
		}
	}
	return nil
}
