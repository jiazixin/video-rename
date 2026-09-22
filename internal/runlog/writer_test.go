package runlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestOpenCreatesRunDir(t *testing.T) {
	base := t.TempDir()
	w, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// 目录名形如 20060102T150405Z-<12 hex>
	re := regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{12}$`)
	if !re.MatchString(filepath.Base(w.Dir())) {
		t.Errorf("运行目录名 = %q", w.Dir())
	}
	for _, f := range []string{"success.jsonl", "failure.jsonl"} {
		if _, err := os.Stat(filepath.Join(w.Dir(), f)); err != nil {
			t.Errorf("缺少 %s: %v", f, err)
		}
	}
}

func TestWriteEvents(t *testing.T) {
	base := t.TempDir()
	w, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Pending(Event{JobID: "0001", Mode: "apply", SourcePath: "/v/a.mp4", TargetPath: "/v/t.mp4"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Success(Event{JobID: "0001", Mode: "apply", Status: "renamed", SourcePath: "/v/a.mp4", TargetPath: "/v/t.mp4", ElapsedMS: 100, FramesPlanned: 5, FramesSent: 5, HTTPAttempts: 1, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Failure(Event{JobID: "0002", Mode: "apply", Status: "failed", SourcePath: "/v/b.mkv", Stage: "probe", ErrorCode: "PROBE_NO_VIDEO", ErrorMessage: "no video", ExitCode: 1, StderrTail: "tail"}); err != nil {
		t.Fatal(err)
	}

	successLines := readLines(t, filepath.Join(w.Dir(), "success.jsonl"))
	failureLines := readLines(t, filepath.Join(w.Dir(), "failure.jsonl"))
	if len(successLines) != 1 || len(failureLines) != 2 {
		t.Fatalf("行数异常: success=%d failure=%d", len(successLines), len(failureLines))
	}
	var pend, fail Event
	if err := json.Unmarshal([]byte(failureLines[0]), &pend); err != nil {
		t.Fatal(err)
	}
	if pend.RecordType != "journal" || pend.Status != "pending" || pend.SchemaVersion != 1 || pend.RunID != w.RunID() {
		t.Errorf("pending 事件异常: %+v", pend)
	}
	if err := json.Unmarshal([]byte(successLines[0]), &fail); err != nil {
		t.Fatal(err)
	}
	if fail.RecordType != "result" || fail.Status != "renamed" || fail.Time == "" {
		t.Errorf("success 事件异常: %+v", fail)
	}
	var f2 Event
	json.Unmarshal([]byte(failureLines[1]), &f2)
	if f2.ErrorCode != "PROBE_NO_VIDEO" || f2.StderrTail != "tail" {
		t.Errorf("failure 事件异常: %+v", f2)
	}
}

func TestBrokenStopsWrites(t *testing.T) {
	base := t.TempDir()
	w, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	// 关闭底层文件模拟磁盘故障
	w.success.Close()
	w.failure.Close()
	if err := w.Success(Event{JobID: "1"}); err == nil {
		t.Fatal("写入已关闭文件应失败")
	}
	if w.Broken() == nil {
		t.Error("应进入 broken 状态")
	}
	// 后续写入立即失败
	if err := w.Failure(Event{JobID: "2"}); err == nil {
		t.Error("broken 后写入应立即失败")
	}
}

func TestConcurrentWrites(t *testing.T) {
	base := t.TempDir()
	w, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = w.Success(Event{JobID: "j", ElapsedMS: int64(i)})
		}(i)
	}
	wg.Wait()
	lines := readLines(t, filepath.Join(w.Dir(), "success.jsonl"))
	if len(lines) != 32 {
		t.Errorf("并发写入丢失: %d", len(lines))
	}
	for _, l := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("并发写入产生损坏行: %v: %q", err, l)
		}
	}
}

func TestPathEscaping(t *testing.T) {
	base := t.TempDir()
	w, _ := Open(base)
	defer w.Close()
	weird := "/v/quote\"back\\slash\nnewline.mp4"
	if err := w.Success(Event{JobID: "1", SourcePath: weird}); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, filepath.Join(w.Dir(), "success.jsonl"))
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("路径未正确转义: %v", err)
	}
	if ev.SourcePath != weird {
		t.Errorf("往返不一致: %q", ev.SourcePath)
	}
	if strings.Contains(lines[0], "\n") {
		t.Error("行内出现裸换行")
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}
