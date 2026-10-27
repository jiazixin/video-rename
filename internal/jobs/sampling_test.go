package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/sampling"
)

func TestFrameSettingsFrozenForEachBatch(t *testing.T) {
	m, root := setup(t)
	store := sampling.Store{DB: m.DB}
	settings := config.DefaultFrameSettings()
	settings.Sampling.Strategy = "duration"
	settings.Sampling.MaxFrames = 7
	settings.Images.MaxDimension = 512
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := m.Create(t.Context(), Request{Targets: []int64{1}, Recursive: true, Key: "frames-first"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	settings.Sampling.MaxFrames = 20
	settings.Images.MaxDimension = 1024
	if err = store.Save(settings); err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.Analyze = func(_ context.Context, c *config.TaskConfig, _ domain.VideoJob, _, _ string) (string, error) {
		calls++
		wantFrames, wantSize := 7, 512
		if calls == 2 {
			wantFrames, wantSize = 20, 1024
		}
		if c.Sampling.MaxFrames != wantFrames || c.Images.MaxDimension != wantSize || c.Sampling.ParsedStrategy != config.StrategyDuration || len(c.Sampling.ParsedDuration) == 0 {
			t.Errorf("wrong snapshot: %+v %+v", c.Sampling, c.Images)
		}
		return "renamed", nil
	}
	m.Execute(t.Context(), id)
	if err = os.WriteFile(filepath.Join(root, "b.mp4"), []byte("new video"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err = m.Create(t.Context(), Request{Targets: []int64{1}, Recursive: true, Key: "frames-next"}, "schedule", 1)
	if err != nil {
		t.Fatal(err)
	}
	m.Execute(t.Context(), id)
	if calls != 2 {
		t.Fatalf("analyze calls=%d", calls)
	}
}
