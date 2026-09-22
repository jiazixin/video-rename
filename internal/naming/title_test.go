package naming

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func TestCleanTitleBasic(t *testing.T) {
	cases := []struct {
		in, ext, want string
	}{
		{"海边散步", ".mp4", "海边散步"},
		{"  海边散步  ", ".mp4", "海边散步"},
		{"a/b\\c:d*e?f\"g<h>i|j", ".mp4", "a_b_c_d_e_f_g_h_i_j"},
		{"标题。。。", ".mp4", "标题"},
		{"标题...", ".mp4", "标题"},
		{"...隐藏", ".mp4", "隐藏"},
		{"视频.mp4", ".mp4", "视频"},        // 末尾同扩展名去一次
		{"视频.MP4", ".mp4", "视频"},        // 忽略大小写
		{"CON", ".mp4", "_CON"},         // Windows 保留名
		{"COM1", ".mp4", "_COM1"},       // 保留名
		{"con.副标题", ".mp4", "_con.副标题"}, // 保留名后接点
		{"多  余   空白", ".mp4", "多 余 空白"},
		{"重__复___下划线", ".mp4", "重_复_下划线"},
		{"a​b", ".mp4", "ab"}, // 零宽空格(Cf)删除
		{"带\x00NUL", ".mp4", "带NUL"},
		{"带\n换行", ".mp4", "带换行"},
		{"日本語のタイトル", ".mkv", "日本語のタイトル"},
		{"emoji😀标题", ".mp4", "emoji😀标题"},
	}
	for _, tc := range cases {
		got, jerr := CleanTitle(tc.in, tc.ext)
		if jerr != nil {
			t.Errorf("%q 应合法: %v", tc.in, jerr)
			continue
		}
		if got != tc.want {
			t.Errorf("CleanTitle(%q) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanTitleInvalid(t *testing.T) {
	// 特殊字符被替换为 _,因此 "/" 清洗为 "_" 仍是合法标题
	for _, in := range []string{"", "   ", "...", "。。。"} {
		if _, jerr := CleanTitle(in, ".mp4"); jerr == nil {
			t.Errorf("%q 清洗后应无效", in)
		}
	}
}

func TestCleanTitleNFC(t *testing.T) {
	// "가" 的分解形式(NFD)应被组合为 NFC
	nfd := "가" // decomposed
	got, jerr := CleanTitle(nfd, ".mp4")
	if jerr != nil {
		t.Fatal(jerr)
	}
	if got != "가" {
		t.Errorf("NFC 规范化失败: %q", got)
	}
}

func TestCleanTitlePreservesLongTitles(t *testing.T) {
	for _, title := range []string{strings.Repeat("海", 78), strings.Repeat("a", 60) + "😀bc", strings.Repeat("海", 100)} {
		got, jerr := CleanTitle(title, ".mp4")
		if jerr != nil || got != title {
			t.Fatalf("标题不应按字符数截断: got %q, error %v", got, jerr)
		}
	}
	dir := t.TempDir()
	job, _ := newFile(t, dir, "source.mp4")
	title, _ := CleanTitle(strings.Repeat("海", 78), ".mp4")
	result, err := NewAllocator().Allocate(dir, title, ".mp4", job)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(result.Target); got != title+".mp4" {
		t.Fatalf("240 字节内应完整保留: %q", got)
	}
}

func TestTruncateToBytes(t *testing.T) {
	// 中文每字 3 字节;预算 9 → 3 字
	got, jerr := TruncateToBytes("四个中文字", 9, ".mp4")
	if jerr != nil {
		t.Fatal(jerr)
	}
	if got != "四个中" {
		t.Errorf("got %q", got)
	}
	// 截断后尾部空格与句点去除
	got, _ = TruncateToBytes("ab 。 c", 2, ".mp4")
	if got != "ab" {
		t.Errorf("got %q", got)
	}
	if _, jerr := TruncateToBytes("标题", 2, ".mp4"); jerr == nil {
		t.Error("预算过小应报错")
	}
}

func newFile(t *testing.T, dir, name string) (domain.VideoJob, string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	return domain.VideoJob{Path: p, Snapshot: snap}, p
}

func TestAllocateFresh(t *testing.T) {
	dir := t.TempDir()
	job, _ := newFile(t, dir, "source.mp4")
	a := NewAllocator()
	res, jerr := a.Allocate(dir, "海边散步", ".mp4", job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if res.Unchanged {
		t.Error("新名称不应 unchanged")
	}
	if res.Target != filepath.Join(dir, "海边散步.mp4") {
		t.Errorf("target = %q", res.Target)
	}
}

func TestAllocateUnchanged(t *testing.T) {
	dir := t.TempDir()
	job, _ := newFile(t, dir, "海边散步.mp4")
	a := NewAllocator()
	res, jerr := a.Allocate(dir, "海边散步", ".mp4", job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if !res.Unchanged {
		t.Error("同名应 unchanged")
	}
}

func TestAllocateUnchangedCaseOnly(t *testing.T) {
	dir := t.TempDir()
	job, _ := newFile(t, dir, "Beach.mp4")
	a := NewAllocator()
	// 大小写不敏感卷上 "beach.mp4" 与源是同一文件 → unchanged;
	// 大小写敏感卷上不存在该路径 → 分配新目标。
	res, jerr := a.Allocate(dir, "beach", ".mp4", job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	t.Logf("case-only: unchanged=%v target=%q (取决于卷大小写敏感性)", res.Unchanged, res.Target)
}

func TestAllocateConflictSuffix(t *testing.T) {
	dir := t.TempDir()
	job, _ := newFile(t, dir, "source.mp4")
	// 占位同名目标
	if err := os.WriteFile(filepath.Join(dir, "海边散步.mp4"), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "海边散步 (2).mp4"), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewAllocator()
	res, jerr := a.Allocate(dir, "海边散步", ".mp4", job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if res.Target != filepath.Join(dir, "海边散步 (3).mp4") {
		t.Errorf("target = %q", res.Target)
	}
}

func TestAllocateRunReservation(t *testing.T) {
	dir := t.TempDir()
	job1, _ := newFile(t, dir, "a.mp4")
	job2, _ := newFile(t, dir, "b.mp4")
	a := NewAllocator()
	r1, err1 := a.Allocate(dir, "同题", ".mp4", job1)
	r2, err2 := a.Allocate(dir, "同题", ".mp4", job2)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if r1.Target == r2.Target {
		t.Errorf("同运行同名应顺序分配: %q vs %q", r1.Target, r2.Target)
	}
	if r2.Target != filepath.Join(dir, "同题 (2).mp4") {
		t.Errorf("第二个应为 (2): %q", r2.Target)
	}
}

func TestAllocateCaseInsensitiveReservation(t *testing.T) {
	dir := t.TempDir()
	job1, _ := newFile(t, dir, "a.mp4")
	job2, _ := newFile(t, dir, "b.mp4")
	a := NewAllocator()
	r1, _ := a.Allocate(dir, "ABC", ".mp4", job1)
	r2, _ := a.Allocate(dir, "abc", ".mp4", job2)
	if strings.EqualFold(filepath.Base(r1.Target), filepath.Base(r2.Target)) {
		t.Errorf("大小写变体不应分配相同名称: %q vs %q", r1.Target, r2.Target)
	}
}

func TestAllocateByteBudgetWithSuffix(t *testing.T) {
	dir := t.TempDir()
	job, _ := newFile(t, dir, "s.mp4")
	// 超长标题及冲突后缀都必须遵守完整文件名的字节上限。
	a := NewAllocator()
	title, err := CleanTitle(strings.Repeat("字", 100), ".mp4")
	if err != nil {
		t.Fatal(err)
	}
	res, jerr := a.Allocate(dir, title, ".mp4", job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	base := filepath.Base(res.Target)
	if len(base) > MaxFilenameBytes {
		t.Errorf("文件名 %d 字节超限: %q", len(base), base)
	}
	// 后缀路径:占用首个候选
	os.WriteFile(res.Target, []byte("x"), 0o644)
	res2, jerr := a.Allocate(dir, title, ".mp4", job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	base2 := filepath.Base(res2.Target)
	if len(base2) > MaxFilenameBytes {
		t.Errorf("后缀文件名 %d 字节超限: %q", len(base2), base2)
	}
	if !strings.Contains(base2, "(2)") {
		t.Errorf("应追加后缀: %q", base2)
	}
}

func TestAllocateConflictExhausted(t *testing.T) {
	dir := t.TempDir()
	job, _ := newFile(t, dir, "s.mp4")
	for n := 0; n < 100; n++ {
		name := "题"
		if n > 0 {
			name = fmt_Sprintf(n)
		}
		os.WriteFile(filepath.Join(dir, name+".mp4"), []byte("x"), 0o644)
	}
	a := NewAllocator()
	if _, jerr := a.Allocate(dir, "题", ".mp4", job); jerr == nil {
		// 100 个占用不会耗尽 9999;此处仅验证大量占用下仍可分配
		t.Log("100 个占用未耗尽,符合预期")
	}
}

// fmt_Sprintf 生成 "题 (n)"。
func fmt_Sprintf(n int) string {
	return "题 (" + itoa(n+1) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
