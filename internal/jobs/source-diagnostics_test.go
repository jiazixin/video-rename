package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

func TestSourceChangeWhileWaitingPersistsDiagnostics(t *testing.T) {
	m, root := setup(t)
	path := filepath.Join(root, "a.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Reserve every slot so the real processing path must wait after extraction.
	release1, _, err := m.modelSlot(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer release1()
	release2, _, err := m.modelSlot(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release2()
		}
	}()
	m.Prepare = func(_ context.Context, _ *config.Config, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		return fakeFrames(work)
	}
	var calls atomic.Int32
	m.Infer = func(context.Context, *config.Config, []domain.Frame) (string, error) {
		calls.Add(1)
		return "unexpected", nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "source-diagnostics"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); m.Execute(ctx, id) }()
	defer func() { cancel(); <-done }()
	for {
		var count int
		if err := m.DB.QueryRow("SELECT count(*) FROM extraction_records WHERE status='success'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("extraction did not complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	release2()
	released = true
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("run did not finish")
	}
	var message, detail string
	if err := m.DB.QueryRow("SELECT message,processing_log FROM events WHERE run=? AND category='failure'", id).Scan(&message, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "视频在等待模型期间发生变化") || calls.Load() != 0 {
		t.Fatalf("message=%s model calls=%d", message, calls.Load())
	}
	for _, want := range []string{path, "差异字段：修改时间", "设备号：", "inode：", "大小（字节）：", "模型排队时长：", "已准备截图：1 张", "本次尚未调用模型"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing %q in persisted diagnostic: %s", want, detail)
		}
	}
}
