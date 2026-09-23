// Package config 实现 YAML 配置的加载、严格解码与校验。
// 配置契约以 docs/configuration.md 为准;未知字段、重复 key、非法类型和
// 非法枚举均启动失败。内置保护参数不暴露为配置。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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

// LoggingConfig 日志目录。
type LoggingConfig struct {
	Dir string `yaml:"dir"`
}

// Config 完整配置。带 yaml 标签的字段参与解码;
// 派生字段(yaml:"-")由 Load 在校验时填充。
type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Config struct {
	LegacyLogging bool           `yaml:"-"`
	Listen        string         `yaml:"listen"`
	Port          int            `yaml:"port"`
	Path          string         `yaml:"media-path"`
	TempStorage   string         `yaml:"tmp-storage"`
	Storage       string         `yaml:"storage"`
	Auth          AuthConfig     `yaml:"auth"`
	Server        ServerConfig   `yaml:"-"`
	Prompt        string         `yaml:"-"` // Runtime task snapshot; never read from YAML.
	Workers       int            `yaml:"-"`
	Sampling      SamplingConfig `yaml:"-"`
	Images        ImagesConfig   `yaml:"-"`
	Logging       LoggingConfig  `yaml:"logging"`

	// 以下为校验后填充的派生值。
	Endpoint           string        `yaml:"-"`
	TimeoutDuration    time.Duration `yaml:"-"`
	ResolvedLoggingDir string        `yaml:"-"`
	ConfigDir          string        `yaml:"-"` // 配置文件所在目录的绝对路径
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
		{strPtr("30s"), 3},
		{strPtr("2m"), 5},
		{strPtr("10m"), 8},
		{strPtr("30m"), 10},
		{nil, 12},
	}
}

// defaultSizeRules 默认大小分档。
func defaultSizeRules() []SizeRule {
	return []SizeRule{
		{int64Ptr(50), 3},
		{int64Ptr(200), 5},
		{int64Ptr(1024), 8},
		{nil, 12},
	}
}

// defaults 返回内置默认值(省略字段取这些值)。
func defaults() *Config {
	return &Config{
		Listen: "127.0.0.1", Port: 8790, Storage: "../data", TempStorage: "../.cache",
		Workers: 2,
		Sampling: SamplingConfig{
			Strategy:      string(StrategyHybrid),
			MaxFrames:     12,
			SkipStart:     0.05,
			SkipEnd:       0.05,
			DurationRules: defaultDurationRules(),
			SizeRules:     defaultSizeRules(),
		},
		Images: ImagesConfig{
			MaxDimension: 768,
			JPEGQuality:  4,
		},
		Logging: LoggingConfig{Dir: "./logs"},
	}
}

func strPtr(s string) *string { return &s }
func int64Ptr(v int64) *int64 { return &v }

// Load 读取并校验配置文件。任何校验失败均返回错误,错误信息汇总全部问题。
// 相对 logging.dir 以配置文件所在目录解析。
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("配置文件路径为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("无法确定配置文件绝对路径: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	cfg := defaults()
	if err := unmarshalStrict(data, cfg); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	var keys map[string]yaml.Node
	_ = yaml.Unmarshal(data, &keys)
	_, cfg.LegacyLogging = keys["logging"]
	cfg.ConfigDir = filepath.Dir(abs)
	if errs := cfg.validate(); len(errs) > 0 {
		return nil, fmt.Errorf("配置校验失败: %s", strings.Join(errs, "; "))
	}
	return cfg, nil
}

// unmarshalStrict 严格解码:拒绝未知字段、重复 key。
func unmarshalStrict(data []byte, cfg *Config) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if root.Kind == 0 { // 空文档
		return nil
	}
	if err := checkDuplicateKeys(&root); err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(cfg)
}

// checkDuplicateKeys 递归检查 YAML 映射中的重复 key。
func checkDuplicateKeys(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) > 0 {
			return checkDuplicateKeys(n.Content[0])
		}
	case yaml.MappingNode:
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			key := k.Value
			if line, dup := seen[key]; dup {
				return fmt.Errorf("重复的 key %q(第 %d 行已定义,第 %d 行再次出现)", key, line, k.Line)
			}
			seen[key] = k.Line
			if err := checkDuplicateKeys(n.Content[i+1]); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if err := checkDuplicateKeys(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// validate 校验全部字段并填充派生值;返回违规列表。
func (c *Config) validate() []string {
	var errs []string

	// workers
	if c.Workers < 1 || c.Workers > 32 {
		errs = append(errs, "workers 必须在 1–32 之间")
	}

	errs = append(errs, c.validateSampling()...)

	// logging
	if strings.TrimSpace(c.Logging.Dir) == "" {
		errs = append(errs, "logging.dir 必须非空")
	} else {
		dir := c.Logging.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(c.ConfigDir, dir)
		}
		c.ResolvedLoggingDir = filepath.Clean(dir)
	}
	return errs
}

func (c *Config) validateSampling() []string {
	var errs []string
	// sampling
	switch c.Sampling.Strategy {
	case string(StrategyHybrid), string(StrategySize), string(StrategyDuration):
		c.Sampling.ParsedStrategy = Strategy(c.Sampling.Strategy)
	default:
		errs = append(errs, fmt.Sprintf("sampling.strategy %q 必须是 hybrid/size/duration 之一", c.Sampling.Strategy))
		c.Sampling.ParsedStrategy = StrategyHybrid
	}
	if c.Sampling.MaxFrames < 1 || c.Sampling.MaxFrames > 32 {
		errs = append(errs, "sampling.max_frames 必须在 1–32 之间")
	}
	if math.IsNaN(c.Sampling.SkipStart) || math.IsInf(c.Sampling.SkipStart, 0) ||
		math.IsNaN(c.Sampling.SkipEnd) || math.IsInf(c.Sampling.SkipEnd, 0) {
		errs = append(errs, "sampling 跳过比例不是有限数值")
	} else {
		if c.Sampling.SkipStart < 0 || c.Sampling.SkipEnd < 0 {
			errs = append(errs, "sampling 跳过比例必须为非负数")
		}
		if c.Sampling.SkipStart+c.Sampling.SkipEnd >= 1 {
			errs = append(errs, "sampling.skip_start_ratio 与 skip_end_ratio 之和必须小于 1")
		}
	}

	pd, derr := parseDurationRules(c.Sampling.DurationRules)
	c.Sampling.ParsedDuration = pd
	errs = append(errs, derr...)
	ps, serr := parseSizeRules(c.Sampling.SizeRules)
	c.Sampling.ParsedSize = ps
	errs = append(errs, serr...)

	// images
	if c.Images.MaxDimension < 128 || c.Images.MaxDimension > 2048 {
		errs = append(errs, "images.max_dimension 必须在 128–2048 之间")
	}
	if c.Images.JPEGQuality < 2 || c.Images.JPEGQuality > 15 {
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

// PromptHash 返回 prompt 的短哈希,用于日志复现;不记录 prompt 原文。
func (c *Config) PromptHash() string {
	return shortHash(c.Prompt)
}

func shortHash(s string) string {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return strconv.FormatUint(h, 16)
}
