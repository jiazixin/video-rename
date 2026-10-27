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
	t.Cleanup(func() { f.Close() })
	return int(f.Fd())
}

func TestNoReplaceAtSuccess(t *testing.T) {
	dir, fd := setup(t)
	if jerr := NoReplaceAt(fd, "source.mp4", "target.mp4"); jerr != nil {
		t.Fatal(jerr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "target.mp4"))
	if err != nil || string(data) != "content" {
		t.Fatalf("目标应存在: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "source.mp4")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("源应不存在")
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
		t.Fatalf("目标存在时必须报 rename_conflict: %v", jerr)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "existing" {
		t.Fatalf("已有目标被覆盖: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "source.mp4")); err != nil {
		t.Error("源文件不应消失")
	}
}

func TestNoReplaceAtMissingSource(t *testing.T) {
	_, fd := setup(t)
	jerr := NoReplaceAt(fd, "nope.mp4", "dst.mp4")
	if jerr == nil {
		t.Fatal("源不存在应失败")
	}
	if jerr.Code != domain.CodeRenameFailed {
		t.Logf("错误码: %s", jerr.Code)
	}
}

func TestNoReplaceAtPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受权限位约束")
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
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	jerr := NoReplaceAt(openDir(t, dir), "src.mp4", "dst.mp4")
	if jerr == nil {
		t.Fatal("只读目录应失败")
	}
	if jerr.Code != domain.CodeRenamePermissionDenied {
		t.Errorf("错误码 = %s, 期望 rename_permission_denied", jerr.Code)
	}
}

func TestNoReplaceAtSameName(t *testing.T) {
	_, fd := setup(t)
	// same name: a no-replace rename onto itself usually reports EEXIST
	if jerr := NoReplaceAt(fd, "source.mp4", "source.mp4"); jerr != nil && jerr.Code != domain.CodeRenameConflict {
		t.Errorf("错误码 = %s", jerr.Code)
	}
}
