package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			m.Analyze = func(_ context.Context, _ *config.Config, job domain.VideoJob, _, _ string) (string, error) {
				if strings.HasPrefix(filepath.Base(job.Path), "bad-") {
					return "", fmt.Errorf("fixture naming failure")
				}
				return "renamed", nil
			}
			m.Prepare = func(_ context.Context, _ *config.Config, job domain.VideoJob, _, _, work string) (domain.FrameSet, error) {
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
			r, err := m.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.want || r.Done != tc.good || r.Failed != tc.bad || r.Skipped != tc.skip {
				t.Fatalf("want %s done=%d failed=%d skipped=%d; got %+v", tc.want, tc.good, tc.bad, tc.skip, r)
			}
		})
	}
}

func TestRecoveryCorrectsOnlyMisclassifiedCompletedBatches(t *testing.T) {
	m, _ := setup(t)
	cases := []struct {
		status                string
		done, skipped, failed int
		want                  string
	}{
		{"partial_failure", 0, 0, 1, "failed"},
		{"partial_failure", 0, 0, 5, "failed"},
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
			r, err := m.Get(ids[i])
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.want || r.Done != tc.done || r.Skipped != tc.skipped || r.Failed != tc.failed || r.Finished != "finished" || r.Error != "original diagnostic" {
				t.Fatalf("case %d altered unexpected fields: %+v", i, r)
			}
		}
	}
}
