package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

func TestBatchCompletionStatus(t *testing.T) {
	for _, tc := range []struct {
		name, task      string
		good, bad, skip int
		want            string
	}{
		{"single_failure", "rename", 0, 1, 0, "failed"},
		{"all_failed", "rename", 0, 2, 0, "failed"},
		{"mixed_success_failure", "rename", 1, 1, 0, "partial_failure"},
		{"mixed_skip_failure", "rename", 0, 1, 1, "partial_failure"},
		{"all_success", "rename", 2, 0, 0, "completed"},
		{"all_skipped", "rename", 0, 0, 1, "completed"},
		{"empty", "rename", 0, 0, 0, "completed"},
		{"extraction_all_failed", "extract", 0, 2, 0, "failed"},
		{"extraction_mixed", "extract", 1, 1, 0, "partial_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, root := setup(t)
			for prefix, count := range map[string]int{"good": tc.good, "bad": tc.bad, "skip": tc.skip} {
				for i := range count {
					if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%s-%d.mp4", prefix, i)), []byte("video"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := m.Library.Refresh(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			if _, err := m.DB.Exec(`INSERT INTO marks(identity,size,mtime,source) SELECT identity,size,mtime,'marked' FROM entries WHERE name LIKE 'skip-%'`); err != nil {
				t.Fatal(err)
			}
			m.Analyze = func(_ context.Context, _ *config.TaskConfig, job domain.VideoJob, _, _ string) (string, error) {
				if strings.HasPrefix(filepath.Base(job.Path), "bad-") {
					return "", fmt.Errorf("fixture naming failure")
				}
				return "renamed", nil
			}
			m.Prepare = func(_ context.Context, _ *config.TaskConfig, job domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
				if strings.HasPrefix(filepath.Base(job.Path), "bad-") {
					return domain.FrameSet{}, fmt.Errorf("fixture extraction failure")
				}
				return fakeFrames(work)
			}
			id, err := m.Create(Request{Task: tc.task, Targets: []int64{1}, Recursive: true, Key: "completion-" + tc.name}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			m.Execute(t.Context(), id)
			r, err := m.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.want || r.Done != tc.good || r.Failed != tc.bad || r.Skipped != tc.skip {
				t.Fatalf("want %s done=%d failed=%d skipped=%d; got %+v", tc.want, tc.good, tc.bad, tc.skip, r)
			}
		})
	}
}

func TestRecoveryPreservesTerminalBatches(t *testing.T) {
	m, _ := setup(t)
	cases := []struct {
		status                string
		done, skipped, failed int
		want                  string
	}{
		{"partial_failure", 0, 0, 1, "partial_failure"},
		{"partial_failure", 0, 0, 5, "partial_failure"},
		{"partial_failure", 1, 0, 1, "partial_failure"},
		{"partial_failure", 0, 1, 1, "partial_failure"},
		{"partial_failure", 0, 0, 0, "partial_failure"},
		{"cancelled", 0, 0, 1, "cancelled"},
		{"interrupted", 0, 0, 1, "interrupted"},
		{"completed", 1, 0, 0, "completed"},
		{"failed", 0, 0, 1, "failed"},
	}
	ids := make([]int64, len(cases))
	for i, tc := range cases {
		res, err := m.DB.Exec(`INSERT INTO runs(source,targets,recursive,status,created,finished,done,skipped,failed,error,request_key) VALUES('manual','[1]',1,?,'created','finished',?,?,?,'original diagnostic',?)`, tc.status, tc.done, tc.skipped, tc.failed, fmt.Sprintf("recovery-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids[i], err = res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := interruptRuns(m.DB); err != nil {
			t.Fatal(err)
		}
		for i, tc := range cases {
			r, err := m.Get(t.Context(), ids[i])
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.want || r.Done != tc.done || r.Skipped != tc.skipped || r.Failed != tc.failed || r.Finished != "finished" || r.Error != "original diagnostic" {
				t.Fatalf("case %d altered unexpected fields: %+v", i, r)
			}
		}
	}
}

func TestCompletionWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name, trigger, status, item string
		done                        int
	}{
		{"item", "BEFORE UPDATE OF status ON run_items WHEN NEW.status='done'", "failed", "cancelled", 0},
		{"counter", "BEFORE UPDATE OF done ON runs WHEN NEW.done>OLD.done", "failed", "cancelled", 0},
		{"terminal", "BEFORE UPDATE OF status ON runs WHEN NEW.status='completed'", "failed", "done", 1},
		{"persistent", "BEFORE UPDATE OF status ON runs WHEN NEW.status IN ('completed','failed','cancelled','partial_failure')", "running", "done", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, root := setup(t)
			if err := os.WriteFile(filepath.Join(root, "source.mp4"), []byte("synthetic video"), 0600); err != nil {
				t.Fatal(err)
			}
			m.Analyze = func(context.Context, *config.TaskConfig, domain.VideoJob, string, string) (string, error) {
				return "new-title", nil
			}
			id, err := m.Create(Request{Targets: []int64{1}, Key: "completion-write-failure"}, "manual", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.DB.Exec("CREATE TRIGGER reject_completion " + tc.trigger + " BEGIN SELECT RAISE(ABORT,'injected storage failure'); END"); err != nil {
				t.Fatal(err)
			}
			m.Execute(t.Context(), id)
			r, err := m.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			var itemStatus string
			if err := m.DB.QueryRow("SELECT status FROM run_items WHERE run=?", id).Scan(&itemStatus); err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.status || itemStatus != tc.item || r.Done != tc.done {
				t.Fatalf("inconsistent completion: run=%+v item=%s", r, itemStatus)
			}
			if tc.status == "failed" && !strings.Contains(r.Error, "injected storage failure") {
				t.Fatalf("lost cause: %+v", r)
			}
			var commits int
			if err := m.DB.QueryRow("SELECT count(*) FROM operations WHERE run=? AND status='committed'", id).Scan(&commits); err != nil || commits != 1 {
				t.Fatalf("lost file journal: %d %v", commits, err)
			}
			if raw, err := os.ReadFile(filepath.Join(root, "new-title.mp4")); err != nil || string(raw) != "synthetic video" {
				t.Fatalf("lost renamed file: %q %v", raw, err)
			}
			if _, err := m.DB.Exec("DROP TRIGGER reject_completion"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := interruptRuns(m.DB); err != nil {
					t.Fatal(err)
				}
			}
			after, err := m.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.status
			if want == "running" {
				want = "interrupted"
			}
			if after.Status != want || after.Done != tc.done {
				t.Fatalf("bad recovery: %+v", after)
			}
		})
	}
}

func TestCompletionRejectsUnfinishedAndRollsBackCancellation(t *testing.T) {
	m, root := setup(t)
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "cancel-write"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.scan(t.Context(), r, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.completeRun(id, false); err == nil {
		t.Fatal("completed with unfinished files")
	}
	if _, err := m.DB.Exec("CREATE TRIGGER reject_cancel BEFORE UPDATE OF status ON runs WHEN NEW.status='cancelled' BEGIN SELECT RAISE(ABORT,'cancel write failed'); END"); err != nil {
		t.Fatal(err)
	}
	if err := m.completeRun(id, true); err == nil {
		t.Fatal("ignored cancellation persistence error")
	}
	var status string
	if err := m.DB.QueryRow("SELECT status FROM run_items WHERE run=?", id).Scan(&status); err != nil || status != "queued" {
		t.Fatalf("cancellation partially committed: %s %v", status, err)
	}
	if _, err := m.DB.Exec("DROP TRIGGER reject_cancel"); err != nil {
		t.Fatal(err)
	}
	if err := m.completeRun(id, true); err != nil {
		t.Fatal(err)
	}
	r, err = m.Get(t.Context(), id)
	if err != nil || r.Status != "cancelled" {
		t.Fatalf("cancel failed: %+v %v", r, err)
	}
}

func TestCompletionFailureCancelsOtherWorkers(t *testing.T) {
	m, root := setup(t)
	for _, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var calls, stopped atomic.Int32
	var runID int64
	secondStarted := make(chan struct{})
	m.Analyze = func(ctx context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _ string) (string, error) {
		n := calls.Add(1)
		if n == 1 {
			select {
			case <-secondStarted:
				return "new-title", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		if n == 2 {
			close(secondStarted)
		}
		// Slot waits use the active run scheduling context, not the request context.
		<-m.currentSchedule(ctx, runID).Done()
		stopped.Add(1)
		return "", ctx.Err()
	}
	id, err := m.Create(Request{Targets: []int64{1}, Key: "concurrent-write-failure"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DB.Exec("CREATE TRIGGER reject_done BEFORE UPDATE OF status ON run_items WHEN NEW.status='done' BEGIN SELECT RAISE(ABORT,'cannot save result'); END"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	runID = id
	m.Execute(ctx, id)
	if ctx.Err() != nil {
		t.Fatal("workers did not stop before timeout")
	}
	r, err := m.Get(t.Context(), id)
	if err != nil || r.Status != "failed" || r.Done != 0 || stopped.Load() == 0 {
		t.Fatalf("failure not propagated: %+v stopped=%d %v", r, stopped.Load(), err)
	}
	var unfinished int
	if err := m.DB.QueryRow("SELECT count(*) FROM run_items WHERE run=? AND status IN ('queued','running')", id).Scan(&unfinished); err != nil || unfinished != 0 {
		t.Fatalf("unfinished=%d %v", unfinished, err)
	}
}
