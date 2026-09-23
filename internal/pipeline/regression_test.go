package pipeline

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"video-rename/internal/domain"
	"video-rename/internal/runlog"
)

func TestRegressionCancellationAllJobs(t *testing.T) {
	cfg := loadConfig(t, "http://unused")
	videos := t.TempDir()
	for i := range 20 {
		writeDummy(t, videos, fmt.Sprintf("v%02d.mp4", i))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	opts := baseOptions(cfg, videos)
	opts.Workers = 1
	sum, code := Run(ctx, opts)
	if code != 1 {
		t.Fatalf("取消退出码=%d", code)
	}
	assertFinalEvents(t, sum, 20)
	if sum.Cancelled != 20 || !sum.consistent() {
		t.Fatalf("20 个待处理文件取消后应全部入账，实际: %+v", sum)
	}
}
func TestRegressionAuthDoesNotStartQueuedJobs(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(401) }))
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)
	videos := t.TempDir()
	for i := range 20 {
		writeDummy(t, videos, fmt.Sprintf("v%02d.mp4", i))
	}
	opts := baseOptions(cfg, videos)
	opts.Workers = 1
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)
	if code != 2 || sum.Failed != 1 || sum.Cancelled != 19 {
		t.Fatalf("摘要异常: code=%d %+v", code, sum)
	}
	assertFinalEvents(t, sum, 20)
	if calls.Load() != 1 {
		t.Fatalf("workers=1 鉴权失败后不得启动队列中的新任务; HTTP 次数=%d, failed=%d cancelled=%d", calls.Load(), sum.Failed, sum.Cancelled)
	}
}
func TestRegressionPendingContainsIdentity(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	srv := titleServer(t, "测试标题")
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)
	videos := t.TempDir()
	src := writeDummy(t, videos, "a.mp4")
	before, err := domain.StatSnapshot(src)
	if err != nil {
		t.Fatal(err)
	}
	opts := baseOptions(cfg, videos)
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(t.Context(), opts)
	if code != 0 {
		t.Fatal(code)
	}
	events := readJSONL(t, filepath.Join(sum.RunDir, "failure.jsonl"))
	t.Logf("pending=%v", events)
	if len(events) != 1 {
		t.Fatal(events)
	}
	raw, err := os.ReadFile(filepath.Join(sum.RunDir, "failure.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var ev runlog.Event
	if err := json.Unmarshal(bytes.TrimSpace(raw), &ev); err != nil {
		t.Fatal(err)
	}
	snapshot := ev.SourceSnapshot
	if snapshot == nil || snapshot.Dev != before.Dev || snapshot.Ino != before.Ino || snapshot.Size != before.Size || !snapshot.ModTime.Equal(before.ModTime) {
		t.Fatalf("pending 快照与原文件不符: %+v / %+v", snapshot, before)
	}
	// dev/ino 在 JSON 中用字符串保留 uint64 精度。
	fields := events[0]["source_snapshot"].(map[string]any)
	if fields["ino"] != strconv.FormatUint(before.Ino, 10) {
		t.Fatal(fields)
	}
}

func TestRegressionCancellationDuringInference(t *testing.T) {
	work := t.TempDir()
	fixture := filepath.Join(work, "f.jpg")
	makeJPEG(t, fixture, 320, 180)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel(); w.WriteHeader(200) }))
	defer srv.Close()
	cfg := loadConfig(t, srv.URL)
	videos := t.TempDir()
	for i := range 20 {
		writeDummy(t, videos, fmt.Sprintf("v%02d.mp4", i))
	}
	opts := baseOptions(cfg, videos)
	opts.Workers = 1
	opts.FFprobePath = fakeProbeScript(t, work, fixture)
	opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
	sum, code := Run(ctx, opts)
	if code != 1 {
		t.Fatalf("取消退出码=%d", code)
	}
	assertFinalEvents(t, sum, 20)
	if sum.Cancelled != 20 || !sum.consistent() {
		t.Fatalf("推理中取消后出现漏记: %+v", sum)
	}
}

// 每个候选恰好一个最终结果；pending 不参加最终结果计数。
func assertFinalEvents(t *testing.T, sum Summary, count int) {
	t.Helper()
	seen := map[string]bool{}
	for _, name := range []string{"success.jsonl", "failure.jsonl"} {
		for _, ev := range readJSONL(t, filepath.Join(sum.RunDir, name)) {
			if ev["record_type"] != "result" {
				continue
			}
			id := ev["job_id"].(string)
			if seen[id] {
				t.Fatalf("重复最终事件 %s", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != count || !sum.consistent() {
		t.Fatalf("最终事件=%d，summary=%+v", len(seen), sum)
	}
}

// 故障发生在真实 Writer 的底层文件被关闭后，覆盖写入错误及 broken 状态。
type faultLog struct {
	*runlog.Writer
	failAt string
	writes int
}

func (f *faultLog) before(kind string) {
	f.writes++
	if kind == f.failAt {
		_ = f.Writer.Close()
	}
}
func (f *faultLog) Pending(ev runlog.Event) error { f.before("pending"); return f.Writer.Pending(ev) }
func (f *faultLog) Success(ev runlog.Event) error { f.before("success"); return f.Writer.Success(ev) }
func (f *faultLog) Failure(ev runlog.Event) error { f.before("failure"); return f.Writer.Failure(ev) }

func TestLogFailureStopsRemainingJobs(t *testing.T) {
	for _, kind := range []string{"pending", "success", "failure"} {
		t.Run(kind, func(t *testing.T) {
			work := t.TempDir()
			fixture := filepath.Join(work, "f.jpg")
			makeJPEG(t, fixture, 320, 180)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, `{"choices":[{"message":{"content":"故障测试"},"finish_reason":"stop"}]}`)
			}))
			defer srv.Close()
			cfg := loadConfig(t, srv.URL)
			videos := t.TempDir()
			for i := 0; i < 20; i++ {
				name := fmt.Sprintf("v%02d.mp4", i)
				if kind == "failure" && i == 0 {
					name = "broken.mp4"
				}
				writeDummy(t, videos, name)
			}
			var stderr bytes.Buffer
			opts := baseOptions(cfg, videos)
			opts.Workers = 1
			opts.Stderr = &stderr
			opts.FFprobePath = fakeProbeScript(t, work, fixture)
			opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
			var logs *faultLog
			sum, code := runWithLogger(t.Context(), opts, func(dir string) (eventLog, error) {
				w, err := runlog.Open(dir)
				logs = &faultLog{Writer: w, failAt: kind}
				return logs, err
			})
			if code != 2 || !sum.consistent() || sum.Cancelled != 19 || !strings.Contains(sum.GlobalAbort, "LOG_WRITE_FAILED") {
				t.Fatalf("code=%d summary=%+v", code, sum)
			}
			if kind == "success" {
				if sum.Renamed != 1 || sum.Failed != 0 || logs.writes != 2 {
					t.Fatalf("改名成功不可改记失败: %+v writes=%d", sum, logs.writes)
				}
				if _, err := os.Stat(filepath.Join(videos, "故障测试.mp4")); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(stderr.String(), "文件已改名但结果日志未确认") {
					t.Fatal(stderr.String())
				}
			} else {
				if sum.Failed != 1 || sum.Renamed != 0 || logs.writes != 1 {
					t.Fatalf("summary=%+v writes=%d", sum, logs.writes)
				}
				if _, err := os.Stat(filepath.Join(videos, "故障测试.mp4")); !os.IsNotExist(err) {
					t.Fatal("不得在 pending 失败后改名")
				}
			}
			wantCalls := int32(1)
			if kind == "failure" {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Fatalf("故障后仍请求模型: calls=%d", calls.Load())
			}
		})
	}
}

func TestCancelledBeforeCommitPreservesSource(t *testing.T) {
	cfg := loadConfig(t, "http://unused")
	dir := t.TempDir()
	src := writeDummy(t, dir, "source.mp4")
	snapshot, err := domain.StatSnapshot(src)
	if err != nil {
		t.Fatal(err)
	}
	w, err := runlog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	c := newCommitter(w, false, "apply", cfg)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.commit(ctx, domain.VideoJob{JobID: "1", Path: src, Snapshot: snapshot}, &jobRecord{started: time.Now(), title: domain.TitleResult{Title: "新标题"}})
	if c.stats().cancelled != 1 {
		t.Fatal(c.stats())
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal(err)
	}
	events := readJSONL(t, filepath.Join(w.Dir(), "failure.jsonl"))
	if len(events) != 1 || events[0]["status"] != "cancelled" {
		t.Fatal(events)
	}
}

type notifyWriter func([]byte) (int, error)

func (w notifyWriter) Write(p []byte) (int, error) { return w(p) }

func TestGlobalFailureWithInFlightJob(t *testing.T) {
	for _, kind := range []string{"auth", "log"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			work := t.TempDir()
			fixture := filepath.Join(work, "f.jpg")
			makeJPEG(t, fixture, 320, 180)
			secondStarted := make(chan struct{})
			stoppedNotice := make(chan struct{})
			var once sync.Once
			var calls atomic.Int32
			var cancelledRequest atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch calls.Add(1) {
				case 1:
					select {
					case <-secondStarted:
					case <-ctx.Done():
						return
					}
					if kind == "auth" {
						w.WriteHeader(403)
						return
					}
				case 2:
					close(secondStarted)
					if kind == "log" {
						select {
						case <-r.Context().Done():
							cancelledRequest.Store(true)
						case <-ctx.Done():
						}
						return
					}
					select {
					case <-stoppedNotice:
					case <-ctx.Done():
						return
					}
				}
				fmt.Fprint(w, `{"choices":[{"message":{"content":"进行中标题"},"finish_reason":"stop"}]}`)
			}))
			defer srv.Close()
			cfg := loadConfig(t, srv.URL)
			videos := t.TempDir()
			for i := range 20 {
				writeDummy(t, videos, fmt.Sprintf("v%02d.mp4", i))
			}
			opts := baseOptions(cfg, videos)
			opts.Workers = 2
			opts.FFprobePath = fakeProbeScript(t, work, fixture)
			opts.FFmpegPath = fakeFFmpegScript(t, work, fixture)
			opts.Stderr = notifyWriter(func(p []byte) (int, error) {
				if strings.Contains(string(p), "全局故障,") {
					once.Do(func() { close(stoppedNotice) })
				}
				return len(p), nil
			})
			sum, code := runWithLogger(ctx, opts, func(dir string) (eventLog, error) {
				w, err := runlog.Open(dir)
				if kind == "log" {
					return &faultLog{Writer: w, failAt: "pending"}, err
				}
				return w, err
			})
			if code != 2 || !sum.consistent() || calls.Load() != 2 || sum.Failed != 1 {
				t.Fatalf("kind=%s code=%d calls=%d summary=%+v", kind, code, calls.Load(), sum)
			}
			if kind == "auth" {
				if sum.Renamed != 1 || sum.Cancelled != 18 {
					t.Fatalf("鉴权故障不应丢弃进行中结果: %+v", sum)
				}
				assertFinalEvents(t, sum, 20)
			} else {
				if sum.Renamed != 0 || sum.Cancelled != 19 {
					t.Fatal(sum)
				}
				// Close 等待服务端看见取消，避免把观察时序当作行为失败。
				srv.Close()
				if !cancelledRequest.Load() {
					t.Fatal("日志故障未取消进行中 HTTP")
				}
			}
		})
	}
}
