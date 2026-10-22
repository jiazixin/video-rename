package config

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Strategy 采样策略枚举。
type Strategy string

const (
	StrategyHybrid   Strategy = "hybrid"
	StrategySize     Strategy = "size"
	StrategyDuration Strategy = "duration"
)

// SamplingConfig 是 Web 设置及任务快照中的抽帧规则，不从部署 YAML 读取。
// Parsed* 为校验后填充的派生值，不写入 JSON。
type SamplingConfig struct {
	Strategy      string         `yaml:"strategy" json:"strategy"`
	MaxFrames     int            `yaml:"max_frames" json:"max_frames"`
	SkipStart     float64        `yaml:"skip_start_ratio" json:"skip_start_ratio"`
	SkipEnd       float64        `yaml:"skip_end_ratio" json:"skip_end_ratio"`
	DurationRules []DurationRule `yaml:"duration_rules" json:"duration_rules"`
	SizeRules     []SizeRule     `yaml:"size_rules" json:"size_rules"`

	ParsedStrategy Strategy             `yaml:"-" json:"-"`
	ParsedDuration []ParsedDurationRule `yaml:"-" json:"-"`
	ParsedSize     []ParsedSizeRule     `yaml:"-" json:"-"`
}

// DurationRule 时长分档;MaxDuration 为 nil 表示兜底档。
type DurationRule struct {
	MaxDuration *string `yaml:"max_duration" json:"max_duration"`
	Frames      int     `yaml:"frames" json:"frames"`
}

// SizeRule 大小分档(MiB);MaxMiB 为 nil 表示兜底档。
type SizeRule struct {
	MaxMiB *int64 `yaml:"max_mib" json:"max_mib"`
	Frames int    `yaml:"frames" json:"frames"`
}

// ImagesConfig 截图尺寸与 JPEG 质量。
type ImagesConfig struct {
	MaxDimension int `yaml:"max_dimension" json:"max_dimension"`
	JPEGQuality  int `yaml:"jpeg_quality" json:"jpeg_quality"`
}

// ParsedDurationRule 解析后的时长档位。
type ParsedDurationRule struct {
	Max    time.Duration // 兜底档为 +Inf
	Frames int
}

// ParsedSizeRule 解析后的大小档位(字节)。
type ParsedSizeRule struct {
	Max    int64 // MiB 换算为字节;兜底档为 MaxInt64
	Frames int
}

// defaultDurationRules 默认时长分档。
func defaultDurationRules() []DurationRule {
	return []DurationRule{
		{new("30s"), 8},
		{new("2m"), 12},
		{new("10m"), 24},
		{new("30m"), 32},
		{nil, 32},
	}
}

// defaultSizeRules 默认大小分档。
func defaultSizeRules() []SizeRule {
	return []SizeRule{
		{new(int64(50)), 3},
		{new(int64(200)), 5},
		{new(int64(1024)), 8},
		{nil, 12},
	}
}

// FrameSettings contains the editable media parameters and immutable task snapshot.
type FrameSettings struct {
	Sampling SamplingConfig `json:"sampling"`
	Images   ImagesConfig   `json:"images"`
}

func DefaultFrameSettings() FrameSettings {
	return FrameSettings{
		Sampling: SamplingConfig{
			Strategy:      string(StrategyDuration),
			MaxFrames:     32,
			SkipStart:     0.05,
			SkipEnd:       0.05,
			DurationRules: defaultDurationRules(),
			SizeRules:     defaultSizeRules(),
		},
		Images: ImagesConfig{MaxDimension: 768, JPEGQuality: 4},
	}
}

func (s *FrameSettings) Validate() error {
	candidate := *s
	if errs := candidate.validateSampling(); len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "；"))
	}
	s.Sampling = candidate.Sampling
	return nil
}

func (s *FrameSettings) validateSampling() []string {
	var errs []string
	// sampling
	switch s.Sampling.Strategy {
	case string(StrategyHybrid), string(StrategySize), string(StrategyDuration):
		s.Sampling.ParsedStrategy = Strategy(s.Sampling.Strategy)
	default:
		errs = append(errs, fmt.Sprintf("sampling.strategy %q 必须是 hybrid/size/duration 之一", s.Sampling.Strategy))
		s.Sampling.ParsedStrategy = StrategyHybrid
	}
	if s.Sampling.MaxFrames < 1 || s.Sampling.MaxFrames > 32 {
		errs = append(errs, "sampling.max_frames 必须在 1–32 之间")
	}
	if math.IsNaN(s.Sampling.SkipStart) || math.IsInf(s.Sampling.SkipStart, 0) ||
		math.IsNaN(s.Sampling.SkipEnd) || math.IsInf(s.Sampling.SkipEnd, 0) {
		errs = append(errs, "sampling 跳过比例不是有限数值")
	} else {
		if s.Sampling.SkipStart < 0 || s.Sampling.SkipEnd < 0 {
			errs = append(errs, "sampling 跳过比例必须为非负数")
		}
		if s.Sampling.SkipStart+s.Sampling.SkipEnd >= 1 {
			errs = append(errs, "sampling.skip_start_ratio 与 skip_end_ratio 之和必须小于 1")
		}
	}

	pd, derr := parseDurationRules(s.Sampling.DurationRules)
	s.Sampling.ParsedDuration = pd
	errs = append(errs, derr...)
	ps, serr := parseSizeRules(s.Sampling.SizeRules)
	s.Sampling.ParsedSize = ps
	errs = append(errs, serr...)

	// images
	if s.Images.MaxDimension < 128 || s.Images.MaxDimension > 2048 {
		errs = append(errs, "images.max_dimension 必须在 128–2048 之间")
	}
	if s.Images.JPEGQuality < 2 || s.Images.JPEGQuality > 15 {
		errs = append(errs, "images.jpeg_quality 必须在 2–15 之间")
	}

	return errs
}

// parseDurationRules 校验时长规则表并转换为绝对上限。
// 规则:表非空;上限为正且严格递增;仅最后一项可以为 null。
func parseDurationRules(rules []DurationRule) ([]ParsedDurationRule, []string) {
	if len(rules) == 0 {
		return nil, []string{"sampling.duration_rules 不能为空"}
	}
	var errs []string
	out := make([]ParsedDurationRule, 0, len(rules))
	prev := time.Duration(0)
	for i, r := range rules {
		last := i == len(rules)-1
		if r.MaxDuration == nil {
			if !last {
				errs = append(errs, fmt.Sprintf("sampling.duration_rules 第 %d 档的 max_duration 为 null,但 null 只允许出现在最后一档", i+1))
			}
			out = append(out, ParsedDurationRule{Max: time.Duration(math.MaxInt64), Frames: r.Frames})
			continue
		}
		d, err := time.ParseDuration(*r.MaxDuration)
		if err != nil {
			errs = append(errs, fmt.Sprintf("sampling.duration_rules 第 %d 档 max_duration %q 不是有效时长", i+1, *r.MaxDuration))
			continue
		}
		if d <= 0 {
			errs = append(errs, fmt.Sprintf("sampling.duration_rules 第 %d 档 max_duration 必须为正数", i+1))
			continue
		}
		if d <= prev {
			errs = append(errs, fmt.Sprintf("sampling.duration_rules 上限必须严格递增(第 %d 档 %s 未大于前档)", i+1, *r.MaxDuration))
			continue
		}
		prev = d
		out = append(out, ParsedDurationRule{Max: d, Frames: r.Frames})
	}
	errs = append(errs, checkRuleTail(rules, func(i int) bool { return rules[i].MaxDuration != nil })...)
	errs = append(errs, checkFrames("sampling.duration_rules", rules, func(i int) int { return rules[i].Frames })...)
	return out, errs
}

// parseSizeRules 校验大小规则表;MiB = 1024 × 1024 字节。
func parseSizeRules(rules []SizeRule) ([]ParsedSizeRule, []string) {
	if len(rules) == 0 {
		return nil, []string{"sampling.size_rules 不能为空"}
	}
	var errs []string
	out := make([]ParsedSizeRule, 0, len(rules))
	var prev int64
	for i, r := range rules {
		last := i == len(rules)-1
		if r.MaxMiB == nil {
			if !last {
				errs = append(errs, fmt.Sprintf("sampling.size_rules 第 %d 档的 max_mib 为 null,但 null 只允许出现在最后一档", i+1))
			}
			out = append(out, ParsedSizeRule{Max: math.MaxInt64, Frames: r.Frames})
			continue
		}
		if *r.MaxMiB <= 0 || *r.MaxMiB > math.MaxInt64/(1024*1024) {
			errs = append(errs, fmt.Sprintf("sampling.size_rules 第 %d 档 max_mib 必须为有效的正整数且换算字节后不能溢出", i+1))
			continue
		}
		if prev != 0 && *r.MaxMiB <= prev {
			errs = append(errs, fmt.Sprintf("sampling.size_rules 上限必须严格递增(第 %d 档 %d 未大于前档)", i+1, *r.MaxMiB))
			continue
		}
		prev = *r.MaxMiB
		out = append(out, ParsedSizeRule{Max: *r.MaxMiB * 1024 * 1024, Frames: r.Frames})
	}
	errs = append(errs, checkRuleTail(rules, func(i int) bool { return rules[i].MaxMiB != nil })...)
	errs = append(errs, checkFrames("sampling.size_rules", rules, func(i int) int { return rules[i].Frames })...)
	return out, errs
}

// checkRuleTail 要求表最后一档必须是 null 兜底档。
func checkRuleTail[T any](rules []T, nonNull func(int) bool) []string {
	if len(rules) > 0 && nonNull(len(rules)-1) {
		return []string{"规则表最后一档必须为 null 兜底档"}
	}
	return nil
}

// checkFrames 校验各档 frames 在 1–32。
func checkFrames[T any](name string, rules []T, frames func(int) int) []string {
	var errs []string
	for i := range rules {
		f := frames(i)
		if f < 1 || f > 32 {
			errs = append(errs, fmt.Sprintf("%s 第 %d 档 frames 必须在 1–32 之间", name, i+1))
		}
	}
	return errs
}
