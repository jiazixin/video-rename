// Package config defines deployment settings and runtime task snapshots.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Config contains deployment YAML fields and the directory used to resolve paths.
type Config struct {
	Listen      string     `yaml:"listen"`
	Port        int        `yaml:"port"`
	Path        string     `yaml:"media-path"`
	TempStorage string     `yaml:"tmp-storage"`
	Storage     string     `yaml:"storage"`
	Auth        AuthConfig `yaml:"auth"`
	ConfigDir   string     `yaml:"-"`
}

// Load strictly decodes deployment YAML; ValidateService resolves paths and validates startup settings.
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
	cfg := &Config{Listen: "127.0.0.1", Port: 8790, Storage: "../data", TempStorage: "../.cache"}
	if err := unmarshalStrict(data, cfg); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	cfg.ConfigDir = filepath.Dir(abs)
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
	if err := dec.Decode(cfg); err != nil {
		return err
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("配置文件只能包含一个 YAML 文档")
	}
	return nil
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
