package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/framecache"
)

func TestCacheMustNotDeleteMedia(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "video-rename-frames-v1", "pending-videos")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "precious.mp4")
	if err := os.WriteFile(source, []byte("user media"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Listen: "127.0.0.1", Port: 8790, MediaDirs: []string{root}, CacheDir: base, DataDir: filepath.Join(base, "state"), ConfigDir: base, Auth: config.AuthConfig{Username: "admin", Password: "test-password"}}
	if err := cfg.Validate(); err != nil {
		t.Logf("safely rejected: %v", err)
		return
	}
	cache, err := framecache.New(t.Context(), framecache.Options{Base: cfg.CacheDir, LimitBytes: 1 << 30, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("accepted configuration deleted media at cache startup: %v", err)
	}
}

func TestCacheNamespaceProtectsPersistentPaths(t *testing.T) {
	for _, kind := range []string{"media", "storage", "config", "database-link", "media-link", "storage-link", "cache-link"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			managed := config.FrameCachePath(base)
			protected := filepath.Join(managed, "pending-important")
			media, state, conf := filepath.Join(base, "media"), filepath.Join(base, "state"), filepath.Join(base, "conf")
			for _, path := range []string{protected, media, state, conf} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(protected, "precious")
			if err := os.WriteFile(file, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Listen: "127.0.0.1", Port: 8790, MediaDirs: []string{media}, DataDir: state, CacheDir: base, ConfigDir: conf, Auth: config.AuthConfig{Username: "admin", Password: "test"}}
			link := filepath.Join(base, "link")
			if kind == "media-link" || kind == "storage-link" {
				if err := os.Symlink(protected, link); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "media":
				cfg.MediaDirs = []string{protected}
			case "storage":
				cfg.DataDir = filepath.Join(protected, "not-created", "state")
			case "config":
				cfg.ConfigDir = protected
			case "database-link":
				if err := os.Symlink(file, cfg.DatabasePath()); err != nil {
					t.Fatal(err)
				}
			case "media-link":
				cfg.MediaDirs = []string{link}
			case "storage-link":
				cfg.DataDir = filepath.Join(link, "new-state")
			case "cache-link":
				cfg.CacheDir = filepath.Join(base, "cache-parent")
				if err := os.Mkdir(cfg.CacheDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(managed, config.FrameCachePath(cfg.CacheDir)); err != nil {
					t.Fatal(err)
				}
			}
			if err := cfg.Validate(); err == nil {
				t.Fatal("accepted dangerous cache overlap")
			}
			raw, err := os.ReadFile(file)
			if err != nil || string(raw) != "preserve" {
				t.Fatalf("validation mutated data: %q %v", raw, err)
			}
		})
	}
}

func TestCacheNamespaceAllowsSiblingAndParentLayouts(t *testing.T) {
	for _, suffix := range []string{"media", "video-rename-frames-v1-other/media"} {
		t.Run(suffix, func(t *testing.T) {
			base := t.TempDir()
			media := filepath.Join(base, suffix)
			if err := os.MkdirAll(media, 0o700); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Listen: "127.0.0.1", Port: 8790, MediaDirs: []string{media}, DataDir: filepath.Join(base, "state"), CacheDir: base, ConfigDir: base, Auth: config.AuthConfig{Username: "a", Password: "b"}}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			cache, err := framecache.New(t.Context(), framecache.Options{Base: cfg.CacheDir, LimitBytes: 1 << 20, Logger: slog.New(slog.DiscardHandler)})
			if err != nil {
				t.Fatal(err)
			}
			cache.Close()
			if _, err := os.Stat(media); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCacheNamespaceProtectsSymlinkedConfigFile(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	managed := filepath.Join(config.FrameCachePath(base), "pending-config")
	for _, p := range []string{media, managed} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(managed, "config.yaml")
	raw := []byte("listen: 127.0.0.1\nport: 8790\nmedia_dir:\n  - " + media + "\ndata_dir: " + filepath.Join(base, "state") + "\ncache_dir: " + base + "\nauth:\n  username: a\n  password: b\n")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "config.yaml")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(config.Source{Path: link, Explicit: true, WorkDir: base}); err == nil {
		t.Fatal("accepted config file inside disposable namespace")
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("config changed: %v", err)
	}
}
