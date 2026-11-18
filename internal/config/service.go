package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// FieldError reports an invalid configuration field by its YAML path.
type FieldError struct {
	Field   string // YAML path, e.g. "auth.password"
	Message string
	Err     error // underlying cause; may be nil
}

// Error implements error.
func (e *FieldError) Error() string {
	if e.Err == nil {
		return e.Field + ": " + e.Message
	}
	if e.Message == "" {
		return e.Field + ": " + e.Err.Error()
	}
	return e.Field + ": " + e.Message + ": " + e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *FieldError) Unwrap() error { return e.Err }

func fieldError(field, message string) error {
	return &FieldError{Field: field, Message: message}
}

func fieldErr(field, message string, err error) error {
	return &FieldError{Field: field, Message: message, Err: err}
}

// Validate reports the first invalid field. It reads the filesystem to check
// path relationships but changes neither c nor any file.
func (c Config) Validate() error {
	_, err := c.resolve()
	return err
}

// resolve validates c and returns a copy whose paths are absolute and free of symlinks.
func (c Config) resolve() (Config, error) {
	if err := c.validateFields(); err != nil {
		return Config{}, err
	}
	abs := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.ConfigDir, p)
		}
		return filepath.Clean(p)
	}
	var err error
	c.DataDir, err = resolveDirectory(abs(c.DataDir))
	if err != nil {
		return Config{}, fieldErr("data_dir", "", err)
	}
	c.CacheDir, err = resolveDirectory(abs(c.CacheDir))
	if err != nil {
		return Config{}, fieldErr("cache_dir", "", err)
	}
	database, err := resolveExisting(c.DatabasePath())
	if err != nil {
		return Config{}, fieldErr("data_dir", "resolve database", err)
	}
	configDir, err := filepath.EvalSymlinks(c.ConfigDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration directory: %w", err)
	}
	if c.ConfigFile != "" {
		c.ConfigFile, err = filepath.EvalSymlinks(c.ConfigFile)
		if err != nil {
			return Config{}, fmt.Errorf("resolve configuration file: %w", err)
		}
	}
	if c.Log.File != "" {
		logPath := abs(c.Log.File)
		parent, err := resolveDirectory(filepath.Dir(logPath))
		if err != nil {
			return Config{}, fieldErr("log.file", "", err)
		}
		c.Log.File = filepath.Join(parent, filepath.Base(logPath))
		if err := c.checkLogFile(database); err != nil {
			return Config{}, err
		}
	}
	dirs := make([]string, 0, len(c.MediaDirs))
	for i, path := range c.MediaDirs {
		field := fmt.Sprintf("media_dir[%d]", i)
		if strings.TrimSpace(path) == "" {
			return Config{}, fieldError(field, "must not be empty")
		}
		root, err := filepath.EvalSymlinks(abs(path))
		if err != nil {
			return Config{}, fieldErr(field, "not accessible", err)
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return Config{}, fieldError(field, "must be an existing directory")
		}
		for _, other := range dirs {
			if within(root, other) || within(other, root) {
				return Config{}, fieldError(field, "media directories must not overlap")
			}
		}
		for _, item := range []struct{ field, path string }{
			{"data_dir", c.DataDir}, {"data_dir", database}, {"cache_dir", c.CacheDir}, {"log.file", c.Log.File},
		} {
			if item.path != "" && within(root, item.path) {
				return Config{}, fieldError(item.field, "must be outside media_dir")
			}
		}
		if within(root, configDir) || (c.ConfigFile != "" && within(root, c.ConfigFile)) {
			return Config{}, fieldError(field, "must not contain the configuration file or directory")
		}
		dirs = append(dirs, root)
	}
	c.MediaDirs = dirs
	if err := c.checkFrameCache(database, configDir); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validateFields() error {
	if net.ParseIP(c.Listen) == nil {
		return fieldError("listen", "must be an IP address")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fieldError("port", "must be between 1 and 65535")
	}
	if strings.TrimSpace(c.Auth.Username) == "" {
		return fieldError("auth.username", "is required")
	}
	if strings.TrimSpace(c.Auth.Password.Reveal()) == "" {
		return fieldError("auth.password", "is required")
	}
	// configs/config.example.yaml ships this placeholder password.
	if strings.Contains(c.Auth.Password.Reveal(), "请替换") {
		return fieldError("auth.password", "must not be the example placeholder")
	}
	if len(c.MediaDirs) > 100 {
		return fieldError("media_dir", "at most 100 roots can be selected for scanning")
	}
	if len(c.MediaDirs) == 0 {
		return fieldError("media_dir", "is required")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fieldError("data_dir", "is required")
	}
	if strings.TrimSpace(c.CacheDir) == "" {
		return fieldError("cache_dir", "must not be empty")
	}
	switch c.Log.Level {
	case "", "debug", "info", "warn", "error": // empty selects info; decoding substitutes it
	default:
		return fieldError("log.level", "must be debug, info, warn or error")
	}
	if c.Log.Format != "" && c.Log.Format != "json" && c.Log.Format != "text" { // empty selects json
		return fieldError("log.format", "must be json or text")
	}
	return nil
}

// checkLogFile prevents an append-only log from corrupting deployment state.
func (c Config) checkLogFile(database string) error {
	if within(c.Log.File, c.DataDir) || within(c.Log.File, c.CacheDir) {
		return fieldError("log.file", "cannot be a directory required by data_dir or cache_dir")
	}
	info, err := os.Lstat(c.Log.File)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fieldErr("log.file", "", err)
	}
	if info != nil && !info.Mode().IsRegular() {
		return fieldError("log.file", "must be a regular file")
	}
	protected := []string{c.ConfigFile}
	for _, suffix := range []string{"", ".key", ".lock", "-shm", "-wal", "-journal"} {
		protected = append(protected, database+suffix, c.DatabasePath()+suffix)
	}
	for _, path := range protected {
		if path == "" {
			continue
		}
		if within(path, c.Log.File) {
			return fieldError("log.file", "must differ from configuration and database files")
		}
		if info != nil {
			other, err := os.Stat(path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fieldErr("log.file", "inspect protected file", err)
			}
			if other != nil && os.SameFile(info, other) {
				return fieldError("log.file", "must differ from configuration and database files")
			}
		}
	}
	return nil
}

// resolveExisting follows a symlink at path when one exists, so an existing
// database link is checked before it is opened.
func resolveExisting(path string) (string, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

// checkFrameCache keeps persistent paths out of the disposable cache namespace.
func (c Config) checkFrameCache(database, configDir string) error {
	cachePath := FrameCachePath(c.CacheDir)
	if st, err := os.Lstat(cachePath); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fieldError("cache_dir", "managed frame cache directory must not be a symlink")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fieldErr("cache_dir", "frame cache directory", err)
	}
	cacheRoot, err := resolveDirectory(cachePath)
	if err != nil {
		return fieldErr("cache_dir", "frame cache directory", err)
	}
	// Only the generated cache namespace is disposable. Its parent may also
	// contain configuration or storage, but none may live inside the namespace.
	protected := append([]string{c.DataDir, database, configDir}, c.MediaDirs...)
	if c.Log.File != "" {
		protected = append(protected, c.Log.File)
	}
	if c.ConfigFile != "" {
		protected = append(protected, c.ConfigFile)
	}
	for _, p := range protected {
		if within(cacheRoot, p) {
			return fieldError("cache_dir", fmt.Sprintf("managed frame cache directory %s must not contain media, configuration, data or logs", cacheRoot))
		}
	}
	return nil
}

// within reports whether path is dir or lies below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// FrameCachePath is the disposable namespace shared by validation and cleanup.
func FrameCachePath(base string) string {
	return filepath.Join(base, "video-rename-frames-v1")
}

// DatabasePath keeps the on-disk filename internal; DataDir always denotes a directory.
func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "data.sqlite")
}

// resolveDirectory resolves existing ancestors, including symlinks, without creating data.
func resolveDirectory(path string) (string, error) {
	parent, suffix := path, ""
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", errors.New("invalid directory path")
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		parent = next
	}
	real, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", errors.New("not a directory")
	}
	return filepath.Join(real, suffix), nil
}
