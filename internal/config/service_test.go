package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"video-rename/internal/secret"
)

func TestServiceConfiguration(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	os.Mkdir(media, 0o700)
	cfg, err := loadTemp(t, minimalYAML+"\nlisten: 127.0.0.1\nport: 8790\nmedia_dir:\n  - "+media+"\nauth:\n  username: admin\n  password: test-password-123\ndata_dir: "+filepath.Join(base, "state")+"\n", "cfg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := cfg
	bad.Port = 0
	if err = bad.Validate(); err == nil || err.Error() != "port: must be between 1 and 65535" {
		t.Fatalf("invalid port: %v", err)
	}
	if field, ok := errors.AsType[*FieldError](err); !ok || field.Field != "port" {
		t.Fatalf("not a field error: %#v", err)
	}
	bad = cfg
	bad.DataDir = filepath.Join(media, "data")
	if err = bad.Validate(); err == nil || err.Error() != "data_dir: must be outside media_dir" {
		t.Fatalf("database inside library: %v", err)
	}
	if bad.DataDir != filepath.Join(media, "data") {
		t.Fatal("Validate changed the configuration")
	}
}

func TestValidateDoesNotNormalize(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "media"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Listen: "127.0.0.1", Port: 8790, MediaDirs: []string{"../media"}, DataDir: "../data", CacheDir: "../.cache", ConfigDir: filepath.Join(base, "configs"), Auth: AuthConfig{Username: "admin", Password: "x"}}
	if err := os.Mkdir(cfg.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := cfg
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("Validate changed %+v to %+v", want, cfg)
	}
}
func TestLoggingIsNotDeploymentConfiguration(t *testing.T) {
	if _, err := loadTemp(t, minimalYAML+"logging:\n  dir: ./logs\n", "cfg.yaml"); err == nil || !strings.Contains(err.Error(), "logging") {
		t.Fatal("unsupported configuration accepted", err)
	}
}

func TestStorageDirectoryAndShortPassword(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	if err := os.Mkdir(media, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, storage := range []string{"./custom/nested", filepath.Join(base, "absolute"), ""} {
		t.Run(storage, func(t *testing.T) {
			yaml := "media_dir:\n  - " + media + "\nauth:\n  username: admin\n  password: x\n"
			if storage != "" {
				yaml += "data_dir: " + storage + "\n"
			}
			cfg, err := loadTemp(t, yaml, "cfg.yaml")
			if err != nil {
				t.Fatal(err)
			}
			want := storage
			if want == "" {
				want = "data"
			}
			if !filepath.IsAbs(want) {
				want = filepath.Join(cfg.ConfigDir, want)
			}
			// macOS temp roots can themselves be symlinks.
			if filepath.Base(cfg.DatabasePath()) != "data.sqlite" || filepath.Dir(cfg.DatabasePath()) != cfg.DataDir {
				t.Fatal(cfg.DatabasePath())
			}
			if filepath.Base(cfg.DataDir) != filepath.Base(want) {
				t.Fatal(cfg.DataDir, want)
			}
			again, err := cfg.resolve()
			if err != nil || !reflect.DeepEqual(again, cfg) {
				t.Fatal("resolving a loaded configuration must be idempotent", err)
			}
			for _, password := range []string{"", "   ", "请替换为你的密码"} {
				cfg.Auth.Password = secret.Value(password)
				if cfg.Validate() == nil {
					t.Fatalf("accepted empty/placeholder password %q", password)
				}
			}
		})
	}
}

func TestStorageDirectoryRejectsFilesAndMediaSymlinks(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	os.Mkdir(media, 0o700)
	existing := filepath.Join(base, "old.sqlite")
	os.WriteFile(existing, []byte("preserve"), 0o600)
	link := filepath.Join(base, "linked")
	if err := os.Symlink(media, link); err != nil {
		t.Fatal(err)
	}
	for _, storage := range []string{existing, filepath.Join(existing, "child"), link, filepath.Join(link, "new", "data")} {
		if _, err := loadTemp(t, "media_dir:\n  - "+media+"\ndata_dir: "+storage+"\nauth:\n  username: admin\n  password: x\n", "cfg.yaml"); err == nil || !strings.HasPrefix(err.Error(), "data_dir: ") {
			t.Fatalf("accepted storage %s: %v", storage, err)
		}
	}
	raw, err := os.ReadFile(existing)
	if err != nil || string(raw) != "preserve" {
		t.Fatal("modified old database", err)
	}
}

func TestTempStorageResolutionAndIsolation(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	if err := os.Mkdir(media, 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "media_dir:\n  - " + media + "\ndata_dir: ../state\nauth:\n  username: admin\n  password: test\n"
	cfg, err := loadTemp(t, yaml+"cache_dir: ./frames\n", "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(cfg.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CacheDir != filepath.Join(realDir, "frames") {
		t.Fatal(cfg.CacheDir)
	}
	link := filepath.Join(base, "media-link")
	if err = os.Symlink(media, link); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"\"\"", media, filepath.Join(link, "new-cache")} {
		if _, e := loadTemp(t, yaml+"cache_dir: "+value+"\n", "config.yaml"); e == nil {
			t.Fatal("accepted temp path", value)
		}
	}
}

func TestMultipleMediaDirsAndLogPath(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"one", "two", "two/nested"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	src := Source{Path: filepath.Join(base, "config.yaml"), Explicit: true, WorkDir: base}
	load := func(y string) (Config, error) {
		t.Helper()
		if err := os.WriteFile(src.Path, []byte("auth: {username: admin, password: x}\n"+y), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(src)
	}
	const media = "media_dir: [one, two]\n"
	cfg, err := load(media + "log: {file: logs/server.log}\n")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.MediaDirs, []string{filepath.Join(actual, "one"), filepath.Join(actual, "two")}) || cfg.Log.File != filepath.Join(actual, "logs", "server.log") || cfg.DataDir != filepath.Join(actual, "data") || cfg.CacheDir != filepath.Join(actual, "cache") {
		t.Fatalf("incorrect resolved paths: %+v", cfg)
	}
	for _, bad := range []string{
		"media_dir: []", "media_dir: one", "media_dir: [one, '']", "media_dir: [two, two/nested]", "media_dir: [one, ./one]", "media_dir: [one, missing]",
		media + "data_dir: two/data", media + "cache_dir: two/cache", media + "log: {file: two/server.log}", media + "log: {file: config.yaml}", media + "log: {file: data/data.sqlite.key}", media + "log: {file: data/data.sqlite/service.log}", media + "log: {file: cache/video-rename-frames-v1/server.log}",
		"media_path: one", media + "storage: data", media + "tmp_storage: cache",
	} {
		if _, err := load(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
