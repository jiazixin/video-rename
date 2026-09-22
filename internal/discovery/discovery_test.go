package discovery

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func writeFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func jobPaths(res *Result) []string {
	out := make([]string, len(res.Jobs))
	for i, j := range res.Jobs {
		out[i] = j.Path
	}
	return out
}

func TestSingleFileInputAnyExtension(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "lecture.xyz", 100)
	res, err := Discover(p, Options{MaxCandidates: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 1 || res.Jobs[0].Path != p {
		t.Fatalf("显式文件应被纳入: %+v", jobPaths(res))
	}
	if res.Jobs[0].Snapshot.Size != 100 {
		t.Errorf("快照大小异常: %d", res.Jobs[0].Snapshot.Size)
	}
}

func TestDirectoryFiltersByExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.mp4", 1)
	writeFile(t, dir, "b.MKV", 1) // 大写扩展名
	writeFile(t, dir, "c.txt", 1)
	writeFile(t, dir, "d.mov", 1)
	res, err := Discover(dir, Options{MaxCandidates: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 3 {
		t.Fatalf("应发现 3 个视频: %+v", jobPaths(res))
	}
	if res.Filtered != 1 {
		t.Errorf("过滤项 = %d, 期望 1", res.Filtered)
	}
	if !sort.StringsAreSorted(jobPaths(res)) {
		t.Error("清单应按路径排序")
	}
}

func TestRecursiveFlag(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "top.mp4", 1)
	writeFile(t, sub, "inner.mp4", 1)

	res, err := Discover(dir, Options{MaxCandidates: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 1 {
		t.Fatalf("默认不递归应只发现 1 个: %+v", jobPaths(res))
	}

	res, err = Discover(dir, Options{Recursive: true, MaxCandidates: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 2 {
		t.Fatalf("递归应发现 2 个: %+v", jobPaths(res))
	}
}

func TestSymlinksSkipped(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "real.mp4", 1)
	writeFile(t, dir, "other.mp4", 1)
	if err := os.Symlink(target, filepath.Join(dir, "link.mp4")); err != nil {
		t.Skip("平台不支持符号链接")
	}
	linkDir := filepath.Join(dir, "linkdir")
	if err := os.Symlink(dir, linkDir); err != nil {
		t.Skip("平台不支持符号链接")
	}
	res, err := Discover(dir, Options{Recursive: true, MaxCandidates: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 2 {
		t.Fatalf("符号链接应被跳过: %+v", jobPaths(res))
	}
	if res.Symlinks != 2 {
		t.Errorf("符号链接计数 = %d, 期望 2", res.Symlinks)
	}
}

func TestSymlinkRootRejected(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "real.mp4", 1)
	link := filepath.Join(dir, "link.mp4")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("平台不支持符号链接")
	}
	if _, err := Discover(link, Options{MaxCandidates: 10}); err == nil {
		t.Error("符号链接输入应给出明确错误")
	}
}

func TestHardlinkDedup(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.mp4", 10)
	b := filepath.Join(dir, "b.mp4")
	if err := os.Link(a, b); err != nil {
		t.Skip("平台不支持硬链接")
	}
	res, err := Discover(dir, Options{MaxCandidates: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 1 {
		t.Fatalf("硬链接应去重: %+v", jobPaths(res))
	}
	if res.Duplicates != 1 {
		t.Errorf("重复计数 = %d, 期望 1", res.Duplicates)
	}
	if res.Jobs[0].Path != a {
		t.Errorf("应保留排序后首个: %q vs %q", res.Jobs[0].Path, a)
	}
}

func TestFifoSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.mp4", 1)
	fifo := filepath.Join(dir, "pipe.mp4")
	if err := mkFifo(fifo); err != nil {
		t.Skip("平台不支持管道文件")
	}
	res, err := Discover(dir, Options{MaxCandidates: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 1 || res.NonRegular != 1 {
		t.Fatalf("管道应跳过: jobs=%+v nonRegular=%d", jobPaths(res), res.NonRegular)
	}
}

func TestUnreadableSubdirRecorded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 用户不受权限位约束")
	}
	dir := t.TempDir()
	writeFile(t, dir, "ok.mp4", 1)
	secret := filepath.Join(dir, "secret")
	if err := os.Mkdir(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, secret, "hidden.mp4", 1)
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(secret, 0o755) })
	res, err := Discover(dir, Options{Recursive: true, MaxCandidates: 100})
	if err != nil {
		t.Fatalf("子目录不可读不应终止: %v", err)
	}
	if len(res.Jobs) != 1 {
		t.Errorf("可读路径应继续: %+v", jobPaths(res))
	}
	if len(res.ScanErrors) != 1 {
		t.Errorf("应记录 1 个扫描错误: %+v", res.ScanErrors)
	}
}

func TestInputNotExist(t *testing.T) {
	if _, err := Discover(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Error("不存在的输入应报错")
	}
}

func TestMaxCandidates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.mp4", 1)
	writeFile(t, dir, "b.mp4", 1)
	if _, err := Discover(dir, Options{MaxCandidates: 1}); err == nil || !strings.Contains(err.Error(), "分批") {
		t.Errorf("超上限应报错并提示分批: %v", err)
	}
}

func TestZeroCandidatesNormal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", 1)
	res, err := Discover(dir, Options{MaxCandidates: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 0 || res.Filtered != 1 {
		t.Errorf("零候选应正常结束: %+v", res)
	}
}

func TestChineseAndSpacePaths(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "我的 视频 (2024) [蓝光].mp4", 5)
	res, err := Discover(dir, Options{MaxCandidates: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 1 || res.Jobs[0].Path != p {
		t.Fatalf("中文与空格路径异常: %+v", jobPaths(res))
	}
}

func TestSnapshotIdentity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.mp4", 3)
	res, err := Discover(dir, Options{MaxCandidates: 10})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := domain.StatSnapshot(res.Jobs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Jobs[0].Snapshot.Same(snap) {
		t.Error("快照应与重新获取的一致")
	}
}
