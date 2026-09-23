package pipeline

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
)

// ---- 测试用假外部依赖 ----

// makeJPEG 生成真实 JPEG 夹具。
func makeJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	jpeg.Encode(f, img, &jpeg.Options{Quality: 80})
}

// fakeProbeScript 按文件名分发输出的假 ffprobe。
func fakeProbeScript(t *testing.T, dir, fixture string) string {
	t.Helper()
	sh, _ := exec.LookPath("sh")
	normalJSON := `{"streams":[{"index":0,"codec_type":"video","width":320,"height":180,"duration":"20.0","sample_aspect_ratio":"1:1","disposition":{"default":1,"attached_pic":0}}],"format":{"duration":"20.0","size":"1000"}}`
	p := filepath.Join(dir, "ffprobe")
	body := "#!" + sh + "\n" + `for last; do :; done
case "$(basename "$last")" in
  broken*) echo 'Invalid data' >&2; exit 1 ;;
  novideo*) echo '{"streams":[{"index":0,"codec_type":"audio"}],"format":{"duration":"20"}}' ;;
  *) cat <<'JSON'
` + normalJSON + `
JSON
    ;;
esac
exit 0
`
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeFFmpegScript 复制 JPEG 到输出参数的假 ffmpeg。
func fakeFFmpegScript(t *testing.T, dir, fixture string) string {
	t.Helper()
	sh, _ := exec.LookPath("sh")
	p := filepath.Join(dir, "ffmpeg")
	body := "#!" + sh + "\n" + `for last; do :; done
cp "` + fixture + `" "$last"
exit 0
`
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeDummy(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// loadConfig 构造指向假服务的最小配置。
func loadConfig(t *testing.T, baseURL string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	yaml := "# deployment\n"
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.ApplyModel(config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: baseURL, Model: "test-model", Timeout: "2s", Temperature: 0.2, MaxTokens: 128}, cfg.Prompt); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("损坏的 JSONL 行: %v: %q", err, line)
		}
		out = append(out, m)
	}
	return out
}

// titleServer 返回固定标题的假 llama-server。
func titleServer(t *testing.T, title string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(title)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, content)
	}))
}

func baseOptions(cfg *config.Config, path string) Options {
	return Options{
		Config:  cfg,
		Path:    path,
		Workers: 2,
		Stderr:  os.Stderr,
		Stdout:  io.Discard,
	}
}

// ---- 端到端用例 ----

func TestEndToEndRename(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	srv := titleServer(t, "海边散步")
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)

	videos := t.TempDir()
	writeDummy(t, videos, "001.mp4")
	writeDummy(t, videos, "002.mp4")

	opts := baseOptions(cfg, videos)
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)

	if code != 0 {
		t.Fatalf("退出码 = %d (summary=%+v)", code, sum)
	}
	if sum.Discovered != 2 || sum.Renamed != 2 {
		t.Errorf("摘要异常: %+v", sum)
	}
	// 两个同名目标应顺序分配
	if _, err := os.Stat(filepath.Join(videos, "海边散步.mp4")); err != nil {
		t.Errorf("第一个目标不存在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(videos, "海边散步 (2).mp4")); err != nil {
		t.Errorf("第二个目标不存在: %v", err)
	}
	// 日志校验
	success := readJSONL(t, filepath.Join(sum.RunDir, "success.jsonl"))
	if len(success) != 2 {
		t.Fatalf("success 行数 = %d", len(success))
	}
	for _, ev := range success {
		if ev["status"] != "renamed" || ev["record_type"] != "result" {
			t.Errorf("事件异常: %v", ev)
		}
		if ev["frames_planned"] != float64(3) || ev["frames_sent"] != float64(3) {
			t.Errorf("帧数字段异常: %v", ev)
		}
		if ev["prompt_hash"] == "" || ev["model"] == "" {
			t.Errorf("复现字段缺失: %v", ev)
		}
	}
	// 不包含敏感/超量内容
	raw, _ := os.ReadFile(filepath.Join(sum.RunDir, "success.jsonl"))
	if strings.Contains(string(raw), "data:image") || strings.Contains(string(raw), "测试提示词") {
		t.Error("日志不应包含图片数据或 prompt 原文")
	}
}

func TestEndToEndDryRun(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	srv := titleServer(t, "海边散步")
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)

	videos := t.TempDir()
	writeDummy(t, videos, "001.mp4")
	writeDummy(t, videos, "002.mp4")

	opts := baseOptions(cfg, videos)
	opts.DryRun = true
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)

	if code != 0 {
		t.Fatalf("退出码 = %d", code)
	}
	if sum.Previewed != 2 || sum.Renamed != 0 {
		t.Errorf("dry-run 摘要异常: %+v", sum)
	}
	// 源文件不被改名
	for _, n := range []string{"001.mp4", "002.mp4"} {
		if _, err := os.Stat(filepath.Join(videos, n)); err != nil {
			t.Errorf("源文件 %s 不应被改名: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(videos, "海边散步.mp4")); !os.IsNotExist(err) {
		t.Error("dry-run 不应产生目标文件")
	}
	// 预览不写 pending
	failure := readJSONL(t, filepath.Join(sum.RunDir, "failure.jsonl"))
	if len(failure) != 0 {
		t.Errorf("dry-run 不应有 failure 事件: %v", failure)
	}
}

func TestEndToEndMixedFailures(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	srv := titleServer(t, "海边散步")
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)

	videos := t.TempDir()
	writeDummy(t, videos, "good.mp4")
	writeDummy(t, videos, "broken.mp4")
	writeDummy(t, videos, "novideo.mp4")
	writeDummy(t, videos, "notes.txt") // 过滤

	opts := baseOptions(cfg, videos)
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)

	if code != 1 {
		t.Fatalf("有失败时退出码 = %d, 期望 1", code)
	}
	if sum.Failed != 2 || sum.Renamed != 1 {
		t.Errorf("摘要异常: %+v", sum)
	}
	if sum.Filtered != 1 {
		t.Errorf("过滤计数 = %d", sum.Filtered)
	}
	failure := readJSONL(t, filepath.Join(sum.RunDir, "failure.jsonl"))
	codes := map[string]int{}
	journal := 0
	for _, ev := range failure {
		if ev["record_type"] == "journal" {
			journal++ // rename_pending 审计事件,不计入失败
			continue
		}
		if code, ok := ev["error_code"].(string); ok {
			codes[code]++
		}
	}
	if codes["PROBE_EXEC_FAILED"] != 1 || codes["PROBE_NO_VIDEO"] != 1 {
		t.Errorf("错误码分布异常: %v", codes)
	}
	if journal != 1 {
		t.Errorf("应有 1 条 pending 审计事件(成功改名的), got %d", journal)
	}
	if !sum.consistent() {
		t.Error("计数不变量破坏")
	}
}

func TestEndToEndUnchanged(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	srv := titleServer(t, "海边散步")
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)

	videos := t.TempDir()
	writeDummy(t, videos, "海边散步.mp4")

	opts := baseOptions(cfg, videos)
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)

	if code != 0 {
		t.Fatalf("退出码 = %d", code)
	}
	if sum.Unchanged != 1 || sum.Renamed != 0 {
		t.Errorf("unchanged 摘要异常: %+v", sum)
	}
}

func TestWorkersBound(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)

	var max int64
	var cur atomic.Int64
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := cur.Add(1)
		mu.Lock()
		if n > max {
			max = n
		}
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		cur.Add(-1)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"并发标题"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)

	videos := t.TempDir()
	for i := range 4 {
		writeDummy(t, videos, fmt.Sprintf("v%d.mp4", i))
	}
	opts := baseOptions(cfg, videos)
	opts.Workers = 1
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)
	if code != 0 {
		t.Fatalf("退出码 = %d (%+v)", code, sum)
	}
	mu.Lock()
	m := max
	mu.Unlock()
	if m > 1 {
		t.Errorf("workers=1 时并发 HTTP 峰值 = %d", m)
	}
}

func TestAuthFailureAborts(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)

	videos := t.TempDir()
	for i := range 5 {
		writeDummy(t, videos, fmt.Sprintf("v%d.mp4", i))
	}
	opts := baseOptions(cfg, videos)
	opts.Workers = 1
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)

	if code != 2 {
		t.Fatalf("全局故障退出码 = %d, 期望 2", code)
	}
	if sum.GlobalAbort == "" {
		t.Error("应记录全局故障原因")
	}
	if sum.Renamed != 0 {
		t.Errorf("不应有改名: %+v", sum)
	}
	if !sum.consistent() {
		t.Errorf("计数不变量破坏: %+v", sum)
	}
}

func TestCancellation(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // 阻塞直到测试放行
		fmt.Fprint(w, `{"choices":[{"message":{"content":"取消标题"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	defer close(release)
	cfg := loadConfig(t, srv.URL)

	videos := t.TempDir()
	for i := range 3 {
		writeDummy(t, videos, fmt.Sprintf("v%d.mp4", i))
	}
	opts := baseOptions(cfg, videos)
	opts.Workers = 1
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(300 * time.Millisecond) // 等第一个任务进入推理
		cancel()
	}()
	sum, code := Run(ctx, opts)

	if sum.Renamed != 0 {
		t.Errorf("取消后不应有改名: %+v", sum)
	}
	if !sum.consistent() {
		t.Errorf("计数不变量破坏: %+v", sum)
	}
	failure := readJSONL(t, filepath.Join(sum.RunDir, "failure.jsonl"))
	cancelled := 0
	for _, ev := range failure {
		if ev["status"] == "cancelled" {
			cancelled++
		}
	}
	if cancelled != 3 {
		t.Errorf("取消事件 = %d, 期望 3 (summary=%+v)", cancelled, sum)
	}
	_ = code
}

func TestZeroCandidates(t *testing.T) {
	cfg := loadConfig(t, "http://unused")
	empty := t.TempDir()
	opts := baseOptions(cfg, empty)
	opts.FFprobePath = "ffprobe"
	opts.FFmpegPath = "ffmpeg"
	sum, code := Run(t.Context(), opts)
	if code != 0 || sum.Discovered != 0 {
		t.Errorf("零候选应正常结束: code=%d %+v", code, sum)
	}
}

func TestSourceChangedBeforeCommit(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)

	videos := t.TempDir()
	src := writeDummy(t, videos, "001.mp4")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 返回标题前先外部修改源文件,提交前核验必然发现
		os.WriteFile(src, make([]byte, 2000), 0o644)
		future := time.Now().Add(time.Second)
		os.Chtimes(src, future, future)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"标题已变"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)

	opts := baseOptions(cfg, videos)
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)
	if code != 1 || sum.Failed != 1 {
		t.Fatalf("摘要异常: code=%d %+v", code, sum)
	}
	failure := readJSONL(t, filepath.Join(sum.RunDir, "failure.jsonl"))
	if len(failure) == 0 || failure[len(failure)-1]["error_code"] != "SOURCE_CHANGED" {
		t.Errorf("应报 SOURCE_CHANGED: %v", failure)
	}
	// 源文件保留原名
	if _, err := os.Stat(src); err != nil {
		t.Error("源文件不应消失")
	}
}
