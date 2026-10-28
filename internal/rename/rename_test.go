package rename

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/domain"
)

// setup creates source.mp4 in a fresh directory and returns that directory's
// descriptor, as callers do after verifying the parent path.
func setup(t *testing.T) (string, int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.mp4"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, openDir(t, dir)
}

func openDir(t *testing.T, dir string) int {
	t.Helper()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	return int(f.Fd())
}

func TestNoReplaceAtSuccess(t *testing.T) {
	dir, fd := setup(t)
	if jerr := NoReplaceAt(fd, "source.mp4", "target.mp4"); jerr != nil {
		t.Fatal(jerr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "target.mp4"))
	if err != nil || string(data) != "content" {
		t.Fatalf("got target content %q and error %v, want content and no error", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "source.mp4")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("source still exists after rename")
	}
}

func TestNoReplaceAtNeverOverwrites(t *testing.T) {
	dir, fd := setup(t)
	dst := filepath.Join(dir, "target.mp4")
	if err := os.WriteFile(dst, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	jerr := NoReplaceAt(fd, "source.mp4", "target.mp4")
	if jerr == nil || jerr.Code != domain.CodeRenameConflict {
		t.Fatalf("got %v, want rename conflict for an existing target", jerr)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "existing" {
		t.Fatalf("got existing target content %q and error %v, want unchanged content", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "source.mp4")); err != nil {
		t.Error("source disappeared after conflicting rename")
	}
}

func TestNoReplaceAtMissingSource(t *testing.T) {
	_, fd := setup(t)
	jerr := NoReplaceAt(fd, "nope.mp4", "dst.mp4")
	if jerr == nil {
		t.Fatal("rename with a missing source succeeded")
	}
	if jerr.Code != domain.CodeRenameFailed {
		t.Logf("error code: %s", jerr.Code)
	}
}

func TestNoReplaceAtPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission bits")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Error(err)
		}
	})
	jerr := NoReplaceAt(openDir(t, dir), "src.mp4", "dst.mp4")
	if jerr == nil {
		t.Fatal("rename in a read-only directory succeeded")
	}
	if jerr.Code != domain.CodeRenamePermissionDenied {
		t.Errorf("got error code %s, want rename_permission_denied", jerr.Code)
	}
}

func TestNoReplaceAtSameName(t *testing.T) {
	_, fd := setup(t)
	// same name: a no-replace rename onto itself usually reports EEXIST
	if jerr := NoReplaceAt(fd, "source.mp4", "source.mp4"); jerr != nil && jerr.Code != domain.CodeRenameConflict {
		t.Errorf("unexpected error code %s", jerr.Code)
	}
}
