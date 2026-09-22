package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceConfiguration(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	os.Mkdir(media, 0700)
	cfg, err := loadTemp(t, minimalYAML+"\nlisten: 127.0.0.1\nport: 8790\npath: "+media+"\nauth:\n  username: admin\n  password: test-password-123\nstorage: "+filepath.Join(base, "state")+"\n", "cfg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.ValidateService(); err != nil {
		t.Fatal(err)
	}
	cfg.Port = 0
	if err = cfg.ValidateService(); err == nil {
		t.Fatal("invalid port")
	}
	cfg.Port = 8790
	cfg.Storage = filepath.Join(media, "data")
	if err = cfg.ValidateService(); err == nil {
		t.Fatal("database inside library")
	}
}
func TestLegacyLoggingRejectedByService(t *testing.T) {
	cfg, err := loadTemp(t, minimalYAML+"logging:\n  dir: ./logs\n", "cfg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.ValidateService(); err == nil || !strings.Contains(err.Error(), "logging") {
		t.Fatal(err)
	}
}

func TestStorageDirectoryAndShortPassword(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	if err := os.Mkdir(media, 0700); err != nil {
		t.Fatal(err)
	}
	for _, storage := range []string{"./custom/nested", filepath.Join(base, "absolute"), ""} {
		t.Run(storage, func(t *testing.T) {
			yaml := "path: " + media + "\nauth:\n  username: admin\n  password: x\n"
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
			if err = cfg.ValidateService(); err != nil {
				t.Fatal(err)
			}
			if filepath.Base(cfg.DatabasePath()) != "data.sqlite" || filepath.Dir(cfg.DatabasePath()) != cfg.Storage {
				t.Fatal(cfg.DatabasePath())
			}
			if filepath.Base(cfg.Storage) != filepath.Base(want) {
				t.Fatal(cfg.Storage, want)
			}
			first := cfg.DatabasePath()
			if err = cfg.ValidateService(); err != nil || cfg.DatabasePath() != first {
				t.Fatal("validation must be idempotent", err)
			}
			for _, password := range []string{"", "   ", "请替换为你的密码"} {
				cfg.Auth.Password = password
				if cfg.ValidateService() == nil {
					t.Fatalf("accepted empty/placeholder password %q", password)
				}
			}
		})
	}
}

func TestStorageDirectoryRejectsFilesAndMediaSymlinks(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	os.Mkdir(media, 0700)
	existing := filepath.Join(base, "old.sqlite")
	os.WriteFile(existing, []byte("preserve"), 0600)
	link := filepath.Join(base, "linked")
	if err := os.Symlink(media, link); err != nil {
		t.Fatal(err)
	}
	for _, storage := range []string{existing, filepath.Join(existing, "child"), link, filepath.Join(link, "new", "data")} {
		cfg, err := loadTemp(t, "path: "+media+"\nstorage: "+storage+"\nauth:\n  username: admin\n  password: x\n", "cfg.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ValidateService() == nil {
			t.Fatalf("accepted storage %s", storage)
		}
	}
	raw, err := os.ReadFile(existing)
	if err != nil || string(raw) != "preserve" {
		t.Fatal("modified old database", err)
	}
}
