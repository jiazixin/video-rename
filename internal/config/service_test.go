package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceConfiguration(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	os.Mkdir(media, 0o700)
	cfg, err := loadTemp(t, minimalYAML+"\nlisten: 127.0.0.1\nport: 8790\nmedia_path: "+media+"\nauth:\n  username: admin\n  password: test-password-123\nstorage: "+filepath.Join(base, "state")+"\n", "cfg.yaml")
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
	bad.Storage = filepath.Join(media, "data")
	if err = bad.Validate(); err == nil || err.Error() != "storage: must be outside media_path" {
		t.Fatalf("database inside library: %v", err)
	}
	if bad.Storage != filepath.Join(media, "data") {
		t.Fatal("Validate changed the configuration")
	}
}

func TestValidateDoesNotNormalize(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "media"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Listen: "127.0.0.1", Port: 8790, Path: "../media", Storage: "../data", TempStorage: "../.cache", ConfigDir: filepath.Join(base, "configs"), Auth: AuthConfig{Username: "admin", Password: "x"}}
	if err := os.Mkdir(cfg.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := cfg
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg != want {
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
			yaml := "media_path: " + media + "\nauth:\n  username: admin\n  password: x\n"
			if storage != "" {
				yaml += "storage: " + storage + "\n"
			}
			cfg, err := loadTemp(t, yaml, "cfg.yaml")
			if err != nil {
				t.Fatal(err)
			}
			want := storage
			if want == "" {
				want = "../data"
			}
			if !filepath.IsAbs(want) {
				want = filepath.Join(cfg.ConfigDir, want)
			}
			// macOS temp roots can themselves be symlinks.
			if filepath.Base(cfg.DatabasePath()) != "data.sqlite" || filepath.Dir(cfg.DatabasePath()) != cfg.Storage {
				t.Fatal(cfg.DatabasePath())
			}
			if filepath.Base(cfg.Storage) != filepath.Base(want) {
				t.Fatal(cfg.Storage, want)
			}
			again, err := cfg.resolve()
			if err != nil || again != cfg {
				t.Fatal("resolving a loaded configuration must be idempotent", err)
			}
			for _, password := range []string{"", "   ", "请替换为你的密码"} {
				cfg.Auth.Password = password
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
		if _, err := loadTemp(t, "media_path: "+media+"\nstorage: "+storage+"\nauth:\n  username: admin\n  password: x\n", "cfg.yaml"); err == nil || !strings.HasPrefix(err.Error(), "storage: ") {
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
	yaml := "media_path: " + media + "\nstorage: ../state\nauth:\n  username: admin\n  password: test\n"
	cfg, err := loadTemp(t, yaml+"tmp_storage: ./frames\n", "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(cfg.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TempStorage != filepath.Join(realDir, "frames") {
		t.Fatal(cfg.TempStorage)
	}
	link := filepath.Join(base, "media-link")
	if err = os.Symlink(media, link); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"\"\"", media, filepath.Join(link, "new-cache")} {
		if _, e := loadTemp(t, yaml+"tmp_storage: "+value+"\n", "config.yaml"); e == nil {
			t.Fatal("accepted temp path", value)
		}
	}
}
