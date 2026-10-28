package config

import (
	"math"
	"strconv"
	"time"

	"video-rename/internal/domain"
)

// Strategy is a sampling strategy.
type Strategy string

// Sampling strategies.
const (
	StrategyHybrid   Strategy = "hybrid"
	StrategySize     Strategy = "size"
	StrategyDuration Strategy = "duration"
)

// SamplingConfig holds the frame rules from Web settings and task snapshots; it is never read from the deployment YAML.
// Parsed* fields are derived values filled by validation and not written to JSON.
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

// DurationRule is a duration tier; a nil MaxDurationMS marks the fallback tier.
type DurationRule struct {
	MaxDurationMS *int64 `yaml:"max_duration_ms,omitempty" json:"max_duration_ms,omitzero"`
	Frames        int    `yaml:"frames" json:"frames"`
}

// SizeRule is a size tier in MiB; a nil MaxMiB marks the fallback tier.
type SizeRule struct {
	MaxMiB *int64 `yaml:"max_mib,omitempty" json:"max_mib,omitzero"`
	Frames int    `yaml:"frames" json:"frames"`
}

// ImagesConfig sets frame dimensions and JPEG quality.
type ImagesConfig struct {
	MaxDimension int `yaml:"max_dimension" json:"max_dimension"`
	JPEGQuality  int `yaml:"jpeg_quality" json:"jpeg_quality"`
}

// ParsedDurationRule is a parsed duration tier.
type ParsedDurationRule struct {
	Max    time.Duration // +Inf for the fallback tier
	Frames int
}

// ParsedSizeRule is a parsed size tier in bytes.
type ParsedSizeRule struct {
	Max    int64 // MiB converted to bytes; MaxInt64 for the fallback tier
	Frames int
}

// defaultDurationRules are the default duration tiers.
func defaultDurationRules() []DurationRule {
	return []DurationRule{
		{new(int64(30000)), 8},
		{new(int64(120000)), 12},
		{new(int64(600000)), 24},
		{new(int64(1800000)), 32},
		{nil, 32},
	}
}

// defaultSizeRules are the default size tiers.
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

// DefaultFrameSettings returns the default frame sampling settings.
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

// Validate checks the frame settings and fills their parsed values.
func (s *FrameSettings) Validate() error {
	candidate := *s
	if errs := candidate.validateSampling(); len(errs) > 0 {
		return domain.Invalid(errs...)
	}
	s.Sampling = candidate.Sampling
	return nil
}

func (s *FrameSettings) validateSampling() []domain.FieldError {
	var errs []domain.FieldError
	// sampling
	switch s.Sampling.Strategy {
	case string(StrategyHybrid), string(StrategySize), string(StrategyDuration):
		s.Sampling.ParsedStrategy = Strategy(s.Sampling.Strategy)
	default:
		errs = append(errs, domain.FieldError{Path: "sampling.strategy", Reason: "invalid_choice"})
		s.Sampling.ParsedStrategy = StrategyHybrid
	}
	if s.Sampling.MaxFrames < 1 || s.Sampling.MaxFrames > 32 {
		errs = append(errs, domain.FieldError{Path: "sampling.max_frames", Reason: "out_of_range"})
	}
	if math.IsNaN(s.Sampling.SkipStart) || math.IsInf(s.Sampling.SkipStart, 0) ||
		math.IsNaN(s.Sampling.SkipEnd) || math.IsInf(s.Sampling.SkipEnd, 0) {
		errs = append(errs, domain.FieldError{Path: "sampling.skip_start_ratio", Reason: "not_finite"})
	} else {
		if s.Sampling.SkipStart < 0 || s.Sampling.SkipEnd < 0 {
			errs = append(errs, domain.FieldError{Path: "sampling.skip_start_ratio", Reason: "negative"})
		}
		if s.Sampling.SkipStart+s.Sampling.SkipEnd >= 1 {
			errs = append(errs, domain.FieldError{Path: "sampling.skip_end_ratio", Reason: "sum_too_large"})
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
		errs = append(errs, domain.FieldError{Path: "images.max_dimension", Reason: "out_of_range"})
	}
	if s.Images.JPEGQuality < 2 || s.Images.JPEGQuality > 15 {
		errs = append(errs, domain.FieldError{Path: "images.jpeg_quality", Reason: "out_of_range"})
	}

	return errs
}

// parseDurationRules validates the duration table and converts it to absolute limits.
// Rules: the table is non-empty; limits are positive and strictly increasing; only the last entry may omit its limit.
func parseDurationRules(rules []DurationRule) ([]ParsedDurationRule, []domain.FieldError) {
	if len(rules) == 0 {
		return nil, []domain.FieldError{{Path: "sampling.duration_rules", Reason: "required"}}
	}
	var errs []domain.FieldError
	out := make([]ParsedDurationRule, 0, len(rules))
	prev := time.Duration(0)
	for i, r := range rules {
		last := i == len(rules)-1
		if r.MaxDurationMS == nil {
			if !last {
				errs = append(errs, ruleField("sampling.duration_rules", i, domain.FieldError{Path: "max_duration_ms", Reason: "unbounded_not_last"}))
			}
			out = append(out, ParsedDurationRule{Max: time.Duration(math.MaxInt64), Frames: r.Frames})
			continue
		}
		if *r.MaxDurationMS > math.MaxInt64/int64(time.Millisecond) {
			errs = append(errs, ruleField("sampling.duration_rules", i, domain.FieldError{Path: "max_duration_ms", Reason: "out_of_range"}))
			continue
		}
		if *r.MaxDurationMS <= 0 {
			errs = append(errs, ruleField("sampling.duration_rules", i, domain.FieldError{Path: "max_duration_ms", Reason: "not_positive"}))
			continue
		}
		d := time.Duration(*r.MaxDurationMS) * time.Millisecond
		if d <= prev {
			errs = append(errs, ruleField("sampling.duration_rules", i, domain.FieldError{Path: "max_duration_ms", Reason: "not_increasing"}))
			continue
		}
		prev = d
		out = append(out, ParsedDurationRule{Max: d, Frames: r.Frames})
	}
	errs = append(errs, checkRuleTail("sampling.duration_rules", rules, func(i int) bool { return rules[i].MaxDurationMS != nil })...)
	errs = append(errs, checkFrames("sampling.duration_rules", rules, func(i int) int { return rules[i].Frames })...)
	return out, errs
}

// parseSizeRules validates the size table; MiB = 1024 × 1024 bytes.
func parseSizeRules(rules []SizeRule) ([]ParsedSizeRule, []domain.FieldError) {
	if len(rules) == 0 {
		return nil, []domain.FieldError{{Path: "sampling.size_rules", Reason: "required"}}
	}
	var errs []domain.FieldError
	out := make([]ParsedSizeRule, 0, len(rules))
	var prev int64
	for i, r := range rules {
		last := i == len(rules)-1
		if r.MaxMiB == nil {
			if !last {
				errs = append(errs, ruleField("sampling.size_rules", i, domain.FieldError{Path: "max_mib", Reason: "unbounded_not_last"}))
			}
			out = append(out, ParsedSizeRule{Max: math.MaxInt64, Frames: r.Frames})
			continue
		}
		if *r.MaxMiB <= 0 || *r.MaxMiB > math.MaxInt64/(1024*1024) {
			errs = append(errs, ruleField("sampling.size_rules", i, domain.FieldError{Path: "max_mib", Reason: "out_of_range"}))
			continue
		}
		if prev != 0 && *r.MaxMiB <= prev {
			errs = append(errs, ruleField("sampling.size_rules", i, domain.FieldError{Path: "max_mib", Reason: "not_increasing"}))
			continue
		}
		prev = *r.MaxMiB
		out = append(out, ParsedSizeRule{Max: *r.MaxMiB * 1024 * 1024, Frames: r.Frames})
	}
	errs = append(errs, checkRuleTail("sampling.size_rules", rules, func(i int) bool { return rules[i].MaxMiB != nil })...)
	errs = append(errs, checkFrames("sampling.size_rules", rules, func(i int) int { return rules[i].Frames })...)
	return out, errs
}

// checkRuleTail requires the last tier to be the unbounded fallback tier.
func checkRuleTail[T any](name string, rules []T, nonNull func(int) bool) []domain.FieldError {
	if len(rules) > 0 && nonNull(len(rules)-1) {
		return []domain.FieldError{{Path: name, Reason: "last_not_unbounded"}}
	}
	return nil
}

// ruleField names field of tier i in the rule table name.
func ruleField(name string, i int, issue domain.FieldError) domain.FieldError {
	issue.Path = name + "." + strconv.Itoa(i) + "." + issue.Path
	return issue
}

// checkFrames checks that each tier's frames are within 1–32.
func checkFrames[T any](name string, rules []T, frames func(int) int) []domain.FieldError {
	var errs []domain.FieldError
	for i := range rules {
		f := frames(i)
		if f < 1 || f > 32 {
			errs = append(errs, ruleField(name, i, domain.FieldError{Path: "frames", Reason: "out_of_range"}))
		}
	}
	return errs
}
