package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// minimalYAML 仅包含必填项,其余取默认值。
const minimalYAML = "# deployment\n"

// fullYAML 与 docs/configuration.md 的完整示例一致。
const fullYAML = `

sampling:
  strategy: "hybrid"
  max_frames: 12
  skip_start_ratio: 0.05
  skip_end_ratio: 0.05
  duration_rules:
    - {max_duration: "30s", frames: 3}
    - {max_duration: "2m", frames: 5}
    - {max_duration: "10m", frames: 8}
    - {max_duration: "30m", frames: 10}
    - {max_duration: null, frames: 12}
  size_rules:
    - {max_mib: 50, frames: 3}
    - {max_mib: 200, frames: 5}
    - {max_mib: 1024, frames: 8}
    - {max_mib: null, frames: 12}

images:
  max_dimension: 768
  jpeg_quality: 4

logging:
  dir: "./logs"
`

func loadTemp(t *testing.T, content string, name string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadMinimalAppliesDefaults(t *testing.T) {
	cfg, err := loadTemp(t, minimalYAML, "config.yaml")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	if cfg.Workers != 2 {
		t.Errorf("workers 默认值 = %d, 期望 2", cfg.Workers)
	}
	if cfg.Sampling.MaxFrames != 12 || cfg.Sampling.SkipStart != 0.05 || cfg.Sampling.SkipEnd != 0.05 {
		t.Errorf("sampling 默认值异常: %+v", cfg.Sampling)
	}
	if cfg.Sampling.ParsedStrategy != StrategyHybrid {
		t.Errorf("strategy 默认值 = %v, 期望 hybrid", cfg.Sampling.ParsedStrategy)
	}
	if len(cfg.Sampling.ParsedDuration) != 5 || cfg.Sampling.ParsedDuration[0].Max != 30*time.Second || cfg.Sampling.ParsedDuration[0].Frames != 3 {
		t.Errorf("duration 默认表异常: %+v", cfg.Sampling.ParsedDuration)
	}
	if cfg.Sampling.ParsedDuration[4].Max < time.Duration(1<<62) || cfg.Sampling.ParsedDuration[4].Frames != 12 {
		t.Errorf("duration 兜底档异常: %+v", cfg.Sampling.ParsedDuration[4])
	}
	if len(cfg.Sampling.ParsedSize) != 4 || cfg.Sampling.ParsedSize[2].Max != 1024*1024*1024 {
		t.Errorf("size 默认表异常: %+v", cfg.Sampling.ParsedSize)
	}
	if cfg.Images.MaxDimension != 768 || cfg.Images.JPEGQuality != 4 {
		t.Errorf("images 默认值异常: %+v", cfg.Images)
	}

	// logging.dir 默认 ./logs,相对配置文件目录解析
	want := filepath.Join(cfg.ConfigDir, "logs")
	if cfg.ResolvedLoggingDir != want {
		t.Errorf("logging 目录 = %q, 期望 %q", cfg.ResolvedLoggingDir, want)
	}
}

func TestLoadFullExample(t *testing.T) {
	cfg, err := loadTemp(t, fullYAML, "config.yaml")
	if err != nil {
		t.Fatalf("加载完整示例失败: %v", err)
	}

	if len(cfg.Sampling.ParsedDuration) != 5 || len(cfg.Sampling.ParsedSize) != 4 {
		t.Errorf("规则表长度异常")
	}
	if cfg.Prompt != "" {
		t.Error("YAML 不应填充任务提示词")
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	_, err := loadTemp(t, minimalYAML+"unknown_key: 1\n", "config.yaml")
	if err == nil || !strings.Contains(err.Error(), "unknown_key") {
		t.Fatalf("未知字段应报错, got %v", err)
	}
}

func TestUnknownNestedFieldRejected(t *testing.T) {
	_, err := loadTemp(t, `
server:
  base_url: "http://a/v1"
  model: m
  nope: 1
`, "config.yaml")
	if err == nil {
		t.Fatal("嵌套未知字段应报错")
	}
}

func TestDuplicateKeyRejected(t *testing.T) {
	_, err := loadTemp(t, minimalYAML+"workers: 3\nworkers: 4\n", "config.yaml")
	if err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 key 应报错, got %v", err)
	}
}

func TestDuplicateNestedKeyRejected(t *testing.T) {
	_, err := loadTemp(t, `
server:
  base_url: "http://a/v1"
  base_url: "http://b/v1"
  model: m
`, "config.yaml")
	if err == nil {
		t.Fatal("嵌套重复 key 应报错")
	}
}

func TestInvalidStrategyRejected(t *testing.T) {
	_, err := loadTemp(t, minimalYAML+"sampling:\n  strategy: \"random\"\n", "config.yaml")
	if err == nil || !strings.Contains(err.Error(), "strategy") {
		t.Fatalf("非法枚举应报错, got %v", err)
	}
}

func TestWorkersRemovedFromYAML(t *testing.T) {
	if _, err := loadTemp(t, minimalYAML+"workers: 2\n", "config.yaml"); err == nil {
		t.Fatal("workers must be configured in model profiles")
	}
}

func TestTypeMismatchRejected(t *testing.T) {
	if _, err := loadTemp(t, minimalYAML+"workers: two\n", "config.yaml"); err == nil {
		t.Error("workers 非整数应报错")
	}
	if _, err := loadTemp(t, minimalYAML+"images:\n  max_dimension: large\n", "config.yaml"); err == nil {
		t.Error("max_dimension 非整数应报错")
	}
}

func TestRuleTableValidation(t *testing.T) {
	base := minimalYAML + "sampling:\n"
	cases := map[string]string{
		"显式空表":    base + "  duration_rules: []\n",
		"缺少兜底":    base + "  duration_rules:\n    - {max_duration: \"30s\", frames: 3}\n",
		"非递增":     base + "  duration_rules:\n    - {max_duration: \"2m\", frames: 3}\n    - {max_duration: \"30s\", frames: 5}\n    - {max_duration: null, frames: 8}\n",
		"null在中间": base + "  duration_rules:\n    - {max_duration: null, frames: 3}\n    - {max_duration: \"30s\", frames: 5}\n",
		"帧数越界":    base + "  duration_rules:\n    - {max_duration: \"30s\", frames: 0}\n    - {max_duration: null, frames: 3}\n",
		"大小表缺少兜底": base + "  size_rules:\n    - {max_mib: 50, frames: 3}\n",
		"大小表负值":   base + "  size_rules:\n    - {max_mib: -1, frames: 3}\n    - {max_mib: null, frames: 5}\n",
		"大小表非递增":  base + "  size_rules:\n    - {max_mib: 200, frames: 3}\n    - {max_mib: 50, frames: 5}\n    - {max_mib: null, frames: 8}\n",
	}
	for name, y := range cases {
		if _, err := loadTemp(t, y, "config.yaml"); err == nil {
			t.Errorf("%s 应报错", name)
		}
	}
	// 未使用的表也必须合法:duration 策略下 size_rules 仍要校验
	bad := base + "  strategy: \"duration\"\n  size_rules:\n    - {max_mib: 100, frames: 3}\n    - {max_mib: 50, frames: 5}\n    - {max_mib: null, frames: 8}\n"
	if _, err := loadTemp(t, bad, "config.yaml"); err == nil {
		t.Error("未使用表的违规配置应报错")
	}
}

func TestRuleTableReplacesDefaults(t *testing.T) {
	y := minimalYAML + `sampling:
  duration_rules:
    - {max_duration: "1m", frames: 4}
    - {max_duration: null, frames: 6}
`
	cfg, err := loadTemp(t, y, "config.yaml")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(cfg.Sampling.ParsedDuration) != 2 {
		t.Fatalf("显式填写应整体替换默认表, got %d 档", len(cfg.Sampling.ParsedDuration))
	}
	if cfg.Sampling.ParsedDuration[0].Frames != 4 || cfg.Sampling.ParsedDuration[1].Frames != 6 {
		t.Errorf("替换后的帧数异常: %+v", cfg.Sampling.ParsedDuration)
	}
	if cfg.Sampling.ParsedDuration[1].Max < time.Duration(1<<62) {
		t.Errorf("兜底档应使用极大上限: %v", cfg.Sampling.ParsedDuration[1].Max)
	}
	// size 表保持默认
	if len(cfg.Sampling.ParsedSize) != 4 {
		t.Errorf("未填写的 size 表应保留默认: %d", len(cfg.Sampling.ParsedSize))
	}
}

func TestSkipRatioValidation(t *testing.T) {
	y := minimalYAML + "sampling:\n  skip_start_ratio: 0.6\n  skip_end_ratio: 0.5\n"
	if _, err := loadTemp(t, y, "config.yaml"); err == nil {
		t.Error("跳过比例之和 ≥ 1 应报错")
	}
	y = minimalYAML + "sampling:\n  skip_start_ratio: -0.1\n"
	if _, err := loadTemp(t, y, "config.yaml"); err == nil {
		t.Error("负跳过比例应报错")
	}
}

func TestImagesValidation(t *testing.T) {
	for _, v := range []string{"64", "4096"} {
		y := minimalYAML + "images:\n  max_dimension: " + v + "\n"
		if _, err := loadTemp(t, y, "config.yaml"); err == nil {
			t.Errorf("max_dimension=%s 应报错", v)
		}
	}
	y := minimalYAML + "images:\n  jpeg_quality: 16\n"
	if _, err := loadTemp(t, y, "config.yaml"); err == nil {
		t.Error("jpeg_quality=16 应报错")
	}
}

func TestLoggingDirResolution(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "config.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML+"logging:\n  dir: \"../out\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ResolvedLoggingDir != filepath.Join(dir, "out") {
		t.Errorf("相对目录解析错误: %q", cfg.ResolvedLoggingDir)
	}
	// 绝对路径保持
	if err := os.WriteFile(path, []byte(minimalYAML+"logging:\n  dir: \"/tmp/vr-logs\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ResolvedLoggingDir != "/tmp/vr-logs" {
		t.Errorf("绝对目录应保持: %q", cfg.ResolvedLoggingDir)
	}
}

func TestMissingFileAndEmptyPath(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Error("空路径应报错")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("不存在的配置应报错")
	}
}

func TestPromptHashStable(t *testing.T) {
	cfg, err := loadTemp(t, minimalYAML, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	h1 := cfg.PromptHash()
	cfg2, _ := loadTemp(t, minimalYAML, "config.yaml")
	if cfg2.PromptHash() != h1 {
		t.Error("相同 prompt 哈希应稳定")
	}
}

func TestRejectPromptInYAML(t *testing.T) {
	if _, err := loadTemp(t, minimalYAML+"\nprompt: 不应从配置读取\n", "config.yaml"); err == nil {
		t.Fatal("YAML prompt must be rejected")
	}
}
