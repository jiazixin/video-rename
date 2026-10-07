package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// minimalYAML 仅包含必填项,其余取默认值。
const minimalYAML = "# deployment\n"

// fullYAML 与 docs/configuration.md 的完整示例一致。
const fullYAML = `
listen: "127.0.0.1"
port: 8790
media-path: "/srv/videos"
storage: "../data"
auth:
  username: "admin"
  password: "test-password"
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

	if cfg.Listen != "127.0.0.1" || cfg.Port != 8790 || cfg.Storage != "../data" || cfg.TempStorage != "../.cache" || cfg.ConfigDir == "" {
		t.Fatalf("deployment defaults: %+v", cfg)
	}
}

func TestDefaultFrameSettings(t *testing.T) {
	cfg := DefaultFrameSettings()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Sampling.MaxFrames != 32 || cfg.Sampling.SkipStart != 0.05 || cfg.Sampling.SkipEnd != 0.05 {
		t.Errorf("sampling 默认值异常: %+v", cfg.Sampling)
	}
	if cfg.Sampling.ParsedStrategy != StrategyDuration {
		t.Errorf("strategy 默认值 = %v, 期望 duration", cfg.Sampling.ParsedStrategy)
	}
	if len(cfg.Sampling.ParsedDuration) != 5 || cfg.Sampling.ParsedDuration[0].Max != 30*time.Second || cfg.Sampling.ParsedDuration[0].Frames != 8 {
		t.Errorf("duration 默认表异常: %+v", cfg.Sampling.ParsedDuration)
	}
	if cfg.Sampling.ParsedDuration[4].Max < time.Duration(1<<62) || cfg.Sampling.ParsedDuration[4].Frames != 32 {
		t.Errorf("duration 兜底档异常: %+v", cfg.Sampling.ParsedDuration[4])
	}
	if len(cfg.Sampling.ParsedSize) != 4 || cfg.Sampling.ParsedSize[2].Max != 1024*1024*1024 {
		t.Errorf("size 默认表异常: %+v", cfg.Sampling.ParsedSize)
	}
	if cfg.Images.MaxDimension != 768 || cfg.Images.JPEGQuality != 4 {
		t.Errorf("images 默认值异常: %+v", cfg.Images)
	}

}

func TestLoadFullExample(t *testing.T) {
	cfg, err := loadTemp(t, fullYAML, "config.yaml")
	if err != nil {
		t.Fatalf("加载完整示例失败: %v", err)
	}

	if cfg.Path != "/srv/videos" || cfg.Auth.Username != "admin" || cfg.Auth.Password != "test-password" {
		t.Fatalf("deployment fields: %+v", cfg)
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
	_, err := loadFrameSettings(t, minimalYAML+"sampling:\n  strategy: \"random\"\n")
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
		if _, err := loadFrameSettings(t, y); err == nil {
			t.Errorf("%s 应报错", name)
		}
	}
	// 未使用的表也必须合法:duration 策略下 size_rules 仍要校验
	bad := base + "  strategy: \"duration\"\n  size_rules:\n    - {max_mib: 100, frames: 3}\n    - {max_mib: 50, frames: 5}\n    - {max_mib: null, frames: 8}\n"
	if _, err := loadFrameSettings(t, bad); err == nil {
		t.Error("未使用表的违规配置应报错")
	}
}

func TestRuleTableReplacesDefaults(t *testing.T) {
	y := minimalYAML + `sampling:
  duration_rules:
    - {max_duration: "1m", frames: 4}
    - {max_duration: null, frames: 6}
`
	cfg, err := loadFrameSettings(t, y)
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
	if _, err := loadFrameSettings(t, y); err == nil {
		t.Error("跳过比例之和 ≥ 1 应报错")
	}
	y = minimalYAML + "sampling:\n  skip_start_ratio: -0.1\n"
	if _, err := loadFrameSettings(t, y); err == nil {
		t.Error("负跳过比例应报错")
	}
}

func TestImagesValidation(t *testing.T) {
	for _, v := range []string{"64", "4096"} {
		y := minimalYAML + "images:\n  max_dimension: " + v + "\n"
		if _, err := loadFrameSettings(t, y); err == nil {
			t.Errorf("max_dimension=%s 应报错", v)
		}
	}
	y := minimalYAML + "images:\n  jpeg_quality: 16\n"
	if _, err := loadFrameSettings(t, y); err == nil {
		t.Error("jpeg_quality=16 应报错")
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
	a := TaskConfig{Prompt: "任务要求"}
	b := TaskConfig{Prompt: "任务要求"}
	if a.PromptHash() != b.PromptHash() {
		t.Fatal("identical prompts must have identical hashes")
	}
	b.Prompt = "修改后的任务要求"
	if a.PromptHash() == b.PromptHash() {
		t.Fatal("different prompts must change the hash")
	}
}

func TestRejectPromptInYAML(t *testing.T) {
	if _, err := loadTemp(t, minimalYAML+"\nprompt: 不应从配置读取\n", "config.yaml"); err == nil {
		t.Fatal("YAML prompt must be rejected")
	}
}

// Media validation fixtures decode directly into runtime fields; Load never accepts them.
func loadFrameSettings(t *testing.T, content string) (*FrameSettings, error) {
	t.Helper()
	c := DefaultFrameSettings()
	value := struct {
		Sampling SamplingConfig `yaml:"sampling"`
		Images   ImagesConfig   `yaml:"images"`
	}{c.Sampling, c.Images}
	if err := yaml.Unmarshal([]byte(content), &value); err != nil {
		return nil, err
	}
	c.Sampling, c.Images = value.Sampling, value.Images
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func TestFrameSettingsNotInDeploymentYAML(t *testing.T) {
	for _, field := range []string{"sampling", "images"} {
		if _, err := loadTemp(t, field+": {}\n", "config.yaml"); err == nil {
			t.Fatalf("unexpected deployment field %s", field)
		}
	}
}

func TestMediaPathConfigKey(t *testing.T) {
	cfg, err := loadTemp(t, "media-path: /srv/videos\n", "config.yaml")
	if err != nil || cfg.Path != "/srv/videos" {
		t.Fatalf("media-path 未正确加载: cfg=%+v err=%v", cfg, err)
	}
	if _, err := loadTemp(t, "path: /srv/videos\n", "config.yaml"); err == nil {
		t.Fatal("旧 path 字段应被拒绝")
	}
}

func TestRejectMultipleConfigurationDocuments(t *testing.T) {
	if _, err := loadTemp(t, "port: 8790\n---\nport: 9999\n", "config.yaml"); err == nil {
		t.Fatal("second configuration document silently ignored")
	}
}
