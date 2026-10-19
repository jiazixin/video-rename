package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

// The live stage follows extraction, model attempts and the retry wait, and is
// dropped once the item reaches a final state.
func TestLiveStageFollowsRetries(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.MP4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Prepare = func(_ context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
		return fakeFrames(work)
	}
	var mu sync.Mutex
	var seen []LiveStage
	var item int64
	m.Infer = func(context.Context, *config.TaskConfig, []domain.Frame) (string, error) {
		if err := m.DB.QueryRow("SELECT id FROM run_items").Scan(&item); err != nil {
			t.Error(err)
		}
		mu.Lock()
		seen = append(seen, m.Live([]int64{item})[item])
		attempt := len(seen)
		mu.Unlock()
		if attempt == 1 {
			return "", &domain.JobError{Code: domain.CodeAPITimeout, Message: "响应超时"}
		}
		return "新标题", nil
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "live-retry"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	if len(seen) != 2 {
		t.Fatalf("model calls: %+v", seen)
	}
	for n, s := range seen {
		if s.Stage != "model" || s.Attempt != n+1 || s.Frames != 1 || s.Title != "" || s.SinceMS == 0 {
			t.Fatalf("attempt %d stage: %+v", n+1, s)
		}
	}
	if seen[1].SinceMS < seen[0].SinceMS {
		t.Fatal("stage time did not advance", seen)
	}
	if left := m.Live([]int64{item}); len(left) != 0 {
		t.Fatal("stage kept after finish", left)
	}
	var name string
	if err = m.DB.QueryRow("SELECT name FROM entries WHERE id=2").Scan(&name); err != nil || name != "新标题.mp4" {
		t.Fatal("rename result", name, err)
	}
}

func TestLiveStageTitleAndClear(t *testing.T) {
	var m Manager
	m.setStage(7, "model")
	first := m.Live([]int64{7})[7]
	m.updateStage(7, func(s *LiveStage) { s.Title = "标题.mp4" })
	if got := m.Live([]int64{7, 8}); len(got) != 1 || got[7].Title != "标题.mp4" || got[7].SinceMS != first.SinceMS {
		t.Fatal("same stage must keep its start time", got)
	}
	m.setStage(7, "committing")
	if got := m.Live([]int64{7})[7]; got.Stage != "committing" || got.Title != "标题.mp4" {
		t.Fatal(got)
	}
	m.clearStage(7)
	if got := m.Live([]int64{7}); len(got) != 0 {
		t.Fatal(got)
	}
}
