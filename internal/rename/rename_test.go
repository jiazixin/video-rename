package rename

import (
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/domain"
)

func setup(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, src, filepath.Join(dir, "target.mp4")
}

func TestNoReplaceSuccess(t *testing.T) {
	_, src, dst := setup(t)
	if jerr := NoReplace(src, dst); jerr != nil {
		t.Fatal(jerr)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("目标应存在: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("源应不存在")
	}
}

func TestNoReplaceNeverOverwrites(t *testing.T) {
	_, src, dst := setup(t)
	if err := os.WriteFile(dst, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	jerr := NoReplace(src, dst)
	if jerr == nil {
		t.Fatal("目标存在时必须失败")
	}
	if jerr.Code != domain.CodeRenameConflict {
		t.Errorf("错误码 = %s, 期望 RENAME_CONFLICT", jerr.Code)
	}
	// 原有内容不被覆盖
	data, _ := os.ReadFile(dst)
	if string(data) != "existing" {
		t.Error("已有目标被覆盖!")
	}
	// 源文件保留
	if _, err := os.Stat(src); err != nil {
		t.Error("源文件不应消失")
	}
}

func TestNoReplaceMissingSource(t *testing.T) {
	dir := t.TempDir()
	jerr := NoReplace(filepath.Join(dir, "nope.mp4"), filepath.Join(dir, "dst.mp4"))
	if jerr == nil {
		t.Fatal("源不存在应失败")
	}
	if jerr.Code != domain.CodeRenameFailed {
		t.Logf("错误码: %s", jerr.Code)
	}
}

func TestNoReplacePermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受权限位约束")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	os.WriteFile(src, []byte("x"), 0o644)
	target := filepath.Join(dir, "sub")
	os.Mkdir(target, 0o555) // 只读目录
	t.Cleanup(func() { os.Chmod(target, 0o755) })
	jerr := NoReplace(src, filepath.Join(target, "dst.mp4"))
	if jerr == nil {
		t.Fatal("只读目录应失败")
	}
	if jerr.Code != domain.CodeRenamePermissionDenied {
		t.Errorf("错误码 = %s, 期望 RENAME_PERMISSION_DENIED", jerr.Code)
	}
}

func TestNoReplaceSamePath(t *testing.T) {
	_, src, _ := setup(t)
	jerr := NoReplace(src, src)
	// 同一路径:RENAME_NOREPLACE 对自身通常报 EEXIST
	if jerr != nil && jerr.Code != domain.CodeRenameConflict {
		t.Errorf("错误码 = %s", jerr.Code)
	}
}
