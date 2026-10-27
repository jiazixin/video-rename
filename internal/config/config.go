// Package config defines deployment settings and runtime task snapshots.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// AuthConfig holds the administrator credentials.
type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// LogConfig selects the service log level and format.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn or error
	Format string `yaml:"format"` // json or text
}

// Config contains deployment YAML fields and the directory used to resolve paths.
type Config struct {
	Listen      string     `yaml:"listen"`
	Port        int        `yaml:"port"`
	Path        string     `yaml:"media_path"`
	TempStorage string     `yaml:"tmp_storage"`
	Storage     string     `yaml:"storage"`
	Auth        AuthConfig `yaml:"auth"`
	Log         LogConfig  `yaml:"log"`
	ConfigDir   string     `yaml:"-"` // base for relative paths: the file's directory, or WorkDir without a file
	ConfigFile  string     `yaml:"-"` // absolute file path; empty when built-in defaults are used
}

// Source describes where configuration comes from.
type Source struct {
	Path      string                      // selected file
	Explicit  bool                        // chosen by --config or CONFIG_FILE
	WorkDir   string                      // base for relative paths when no file is used
	LookupEnv func(string) (string, bool) // os.LookupEnv in production; unused: no field has an environment override
}

func defaults() Config {
	return Config{Listen: "127.0.0.1", Port: 8790, Storage: "../data", TempStorage: "../.cache", Log: LogConfig{Level: "info", Format: "json"}}
}

// Load strictly decodes the selected file, resolves its paths and validates the result.
// A missing file that was not chosen explicitly falls back to the built-in defaults,
// which still have to satisfy the required fields.
func Load(src Source) (Config, error) {
	cfg, found, err := read(src)
	if err != nil {
		return Config{}, err
	}
	resolved, err := cfg.resolve()
	if err != nil {
		if !found {
			return Config{}, fmt.Errorf("%s not found and built-in defaults are incomplete: %w", src.Path, err)
		}
		return Config{}, err
	}
	return resolved, nil
}

// read decodes the selected file over the built-in defaults without validating it.
func read(src Source) (cfg Config, found bool, err error) {
	if src.Path == "" {
		return Config{}, false, errors.New("configuration path is empty")
	}
	abs := src.Path
	if !filepath.IsAbs(abs) {
		if !filepath.IsAbs(src.WorkDir) {
			return Config{}, false, fmt.Errorf("resolve %s: work directory %q is not absolute", src.Path, src.WorkDir)
		}
		abs = filepath.Join(src.WorkDir, abs)
	}
	cfg = defaults()
	data, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) && !src.Explicit {
		cfg.ConfigDir = filepath.Clean(src.WorkDir)
		return cfg, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read configuration: %w", err)
	}
	if err := unmarshalStrict(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse %s: %w", abs, err)
	}
	cfg.ConfigDir = filepath.Dir(abs)
	cfg.ConfigFile = abs
	return cfg, true, nil
}

// unmarshalStrict rejects unknown fields, duplicate keys and extra documents.
func unmarshalStrict(data []byte, cfg *Config) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if root.Kind == 0 { // empty document
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
		return errors.New("configuration file must contain a single YAML document")
	}
	return nil
}

// checkDuplicateKeys recursively rejects repeated keys in YAML mappings.
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
				return fmt.Errorf("duplicate key %q on line %d, first defined on line %d", key, k.Line, line)
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
