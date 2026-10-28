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
	c.Path, c.Storage, c.TempStorage = abs(c.Path), abs(c.Storage), abs(c.TempStorage)
	temp, err := resolveDirectory(c.TempStorage)
	if err != nil {
		return Config{}, fieldErr("tmp_storage", "", err)
	}
	c.TempStorage = temp
	root, err := filepath.EvalSymlinks(c.Path)
	if err != nil {
		return Config{}, fieldErr("media_path", "not accessible", err)
	}
	c.Path = root
	if st, err := os.Stat(c.Path); err != nil || !st.IsDir() {
		return Config{}, fieldError("media_path", "must be an existing directory")
	}
	if c.Storage, err = resolveStorage(c.Storage); err != nil {
		return Config{}, err
	}
	database, err := resolveExisting(c.DatabasePath())
	if err != nil {
		return Config{}, fieldErr("storage", "resolve database", err)
	}
	configDir, err := filepath.EvalSymlinks(c.ConfigDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration directory: %w", err)
	}
	for _, p := range []struct{ field, message, path string }{
		{"storage", "must be outside media_path", c.Storage},
		{"storage", "database must be outside media_path", database},
		{"media_path", "must not contain the configuration directory", configDir},
		{"tmp_storage", "must be outside media_path", c.TempStorage},
	} {
		if within(c.Path, p.path) {
			return Config{}, fieldError(p.field, p.message)
		}
	}
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
	if strings.TrimSpace(c.Path) == "" {
		return fieldError("media_path", "is required")
	}
	if strings.TrimSpace(c.Storage) == "" {
		return fieldError("storage", "is required")
	}
	if strings.TrimSpace(c.TempStorage) == "" {
		return fieldError("tmp_storage", "must not be empty")
	}
	switch c.Log.Level {
	case "", "debug", "info", "warn", "error": // empty means info
	default:
		return fieldError("log.level", "must be debug, info, warn or error")
	}
	if c.Log.Format != "" && c.Log.Format != "json" && c.Log.Format != "text" { // empty means json
		return fieldError("log.format", "must be json or text")
	}
	return nil
}

// resolveStorage resolves the nearest existing ancestor so a symlink cannot hide storage inside media.
func resolveStorage(storage string) (string, error) {
	parent, suffix := storage, ""
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fieldErr("storage", "", err)
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		next := filepath.Dir(parent)
		if next == parent {
			return "", fieldError("storage", "invalid path")
		}
		parent = next
	}
	real, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fieldErr("storage", "", err)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", fieldError("storage", "must be a data directory, not a database file name")
	}
	return filepath.Join(real, suffix), nil
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
	cachePath := FrameCachePath(c.TempStorage)
	if st, err := os.Lstat(cachePath); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fieldError("tmp_storage", "managed frame cache directory must not be a symlink")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fieldErr("tmp_storage", "frame cache directory", err)
	}
	cacheRoot, err := resolveDirectory(cachePath)
	if err != nil {
		return fieldErr("tmp_storage", "frame cache directory", err)
	}
	// Only the generated cache namespace is disposable. Its parent may also
	// contain configuration or storage, but none may live inside the namespace.
	protected := []string{c.Path, c.Storage, database, configDir}
	if c.ConfigFile != "" {
		file, err := filepath.EvalSymlinks(c.ConfigFile)
		if err != nil {
			return fmt.Errorf("resolve configuration file: %w", err)
		}
		protected = append(protected, file)
	}
	for _, p := range protected {
		if within(cacheRoot, p) {
			return fieldError("tmp_storage", fmt.Sprintf("managed frame cache directory %s must not contain media, configuration or storage", cacheRoot))
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

// DatabasePath keeps the on-disk filename internal; Storage always denotes a directory.
func (c Config) DatabasePath() string {
	return filepath.Join(c.Storage, "data.sqlite")
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
