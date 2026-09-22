package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// ValidateService resolves deployment paths. Media-only tests may still load Config.
func (c *Config) ValidateService() error {
	if c.LegacyLogging {
		return fmt.Errorf("logging 已移除，业务日志固定保存到 storage 目录中的 SQLite")
	}
	if net.ParseIP(c.Listen) == nil {
		return fmt.Errorf("listen 必须为 IP 地址")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port 必须在 1–65535 之间")
	}
	if strings.TrimSpace(c.Auth.Username) == "" || strings.TrimSpace(c.Auth.Password) == "" || strings.Contains(c.Auth.Password, "请替换") {
		return fmt.Errorf("auth.username 必填，auth.password 必须非空且不能使用示例占位值")
	}
	if strings.TrimSpace(c.Path) == "" || strings.TrimSpace(c.Storage) == "" {
		return fmt.Errorf("path 与 storage 必填")
	}
	resolve := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.ConfigDir, p)
		}
		return filepath.Clean(p)
	}
	c.Path = resolve(c.Path)
	c.Storage = resolve(c.Storage)
	root, err := filepath.EvalSymlinks(c.Path)
	if err != nil {
		return fmt.Errorf("媒体根目录不可访问: %w", err)
	}
	c.Path = root
	st, err := os.Stat(c.Path)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("path 必须是现有目录")
	}
	// Resolve the nearest existing ancestor so a symlink cannot hide storage inside media.
	parent := c.Storage
	suffix := ""
	for {
		_, err = os.Lstat(parent)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		next := filepath.Dir(parent)
		if next == parent {
			return fmt.Errorf("storage 路径无效")
		}
		parent = next
	}
	real, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	st, err = os.Stat(real)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("storage 必须是目录，请填写数据目录而不是数据库文件名")
	}
	c.Storage = filepath.Join(real, suffix)
	// Also check an existing database symlink before opening it.
	database := c.DatabasePath()
	if _, e := os.Lstat(database); e == nil {
		real, e := filepath.EvalSymlinks(database)
		if e != nil {
			return e
		}
		database = real
	} else if !os.IsNotExist(e) {
		return e
	}
	configDir, err := filepath.EvalSymlinks(c.ConfigDir)
	if err != nil {
		return err
	}
	for _, p := range []string{c.Storage, database, configDir} {
		rel, e := filepath.Rel(c.Path, p)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("配置与 storage 不得位于媒体根目录内")
		}
	}
	return nil
}

// DatabasePath keeps the on-disk filename internal; Storage always denotes a directory.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.Storage, "data.sqlite")
}
