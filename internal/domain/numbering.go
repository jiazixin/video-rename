package domain

import (
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

// DefaultMediaNumberingRules returns the default numbering rules of one media
// type.
func DefaultMediaNumberingRules() MediaNumberingRules {
	return MediaNumberingRules{Padding: 1, Lowercase: true, Compact: true}
}

// DefaultNumberingSettings returns the default numbering settings.
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
		{"image", s.Image}, {"audio", s.Audio},
	} {
		if class.rules.Padding < 1 || class.rules.Padding > 9 {
			return InvalidField(class.name+".padding", "out_of_range")
		}
		if len([]rune(class.rules.Prefix)) > 32 {
			return InvalidField(class.name+".prefix", "too_long")
		}
		if strings.ContainsAny(class.rules.Prefix, "/\\\x00") || strings.ContainsFunc(class.rules.Prefix, func(c rune) bool { return c < 0x20 }) {
			return InvalidField(class.name+".prefix", "invalid_character")
		}
		if strings.TrimSpace(class.rules.Prefix) != class.rules.Prefix {
			return InvalidField(class.name+".prefix", "untrimmed")
		}
	}
	return nil
}
