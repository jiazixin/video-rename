//go:build integration

// Integration tests with real ffmpeg and ffprobe.
// Run: go test -tags=integration ./internal/media/
// They skip with a reason when external tools are missing.
package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

func lookTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("missing %s; skipping integration test", name)
	}
	return p
}

// genVideo generates a test video from a lavfi source.
type genVideoOptions struct {
	path  string
	size  string
	d     time.Duration
	extra []string
}

func genVideo(t *testing.T, options genVideoOptions) {
	t.Helper()
	path, size, d, extra := options.path, options.size, options.d, options.extra

	ff := lookTool(t, "ffmpeg")
	args := []string{"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=" + fmtSecs(d) + ":size=" + size + ":rate=10",
		"-pix_fmt", "yuv420p"}
	args = append(args, extra...)
	args = append(args, path)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, ff, args...)
	command.WaitDelay = 5 * time.Second
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate test video failed: %v: %s", err, out)
	}
}

func fmtSecs(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

func TestIntegrationProbeAndExtractLandscape(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "land.mp4")
	genVideo(t, genVideoOptions{path: video, size: "640x360", d: 5 * time.Second, extra: []string{}})

	prober := &Prober{FFprobePath: lookTool(t, "ffprobe")}
	job, _ := fakeJob(t, 1)
	st, _ := domain.StatSnapshot(video)
	job.Path, job.Snapshot = video, st

	meta, jerr := prober.Probe(t.Context(), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.DisplayWidth != 640 || meta.DisplayHeight != 360 {
		t.Fatalf("display dimensions = %dx%d", meta.DisplayWidth, meta.DisplayHeight)
	}
	if meta.Duration != 5*time.Second {
		t.Fatalf("probed duration of the generated 5-second video = %v", meta.Duration)
	}

	s := NewSampler(loadDefaultSampling(t))
	plan, err := s.Plan(meta.Duration, st.Size)
	if err != nil {
		t.Fatal(err)
	}
	// Default duration sampling asks for 8 frames, bounded by the 4 whole
	// seconds in the window after skipping the first and last 5 percent.
	wantTimes := []time.Duration{812500 * time.Microsecond, 1937500 * time.Microsecond, 3062500 * time.Microsecond, 4187500 * time.Microsecond}
	if plan.TargetN != 4 || plan.WindowStart != 250*time.Millisecond || plan.WindowLen != 4500*time.Millisecond || len(plan.Times) != len(wantTimes) {
		t.Fatalf("want 4 frames in [0.25s, 4.75s] for a 5-second video: %+v", plan)
	}
	for i, at := range wantTimes {
		if plan.Times[i] != at {
			t.Errorf("sampling point %d = %v, want %v", i, plan.Times[i], at)
		}
	}

	ex := &Extractor{FFmpegPath: lookTool(t, "ffmpeg"), Images: defaultImages()}
	set, jerr := ex.Extract(t.Context(), job, meta, plan)
	if jerr != nil {
		t.Fatal(jerr)
	}
	defer os.RemoveAll(set.TempDir)
	if len(set.Frames) != 4 {
		t.Fatalf("want 4 extracted frames: %d (failed=%+v warn=%v)", len(set.Frames), set.Failed, set.Warn)
	}
	for i, fr := range set.Frames {
		// 640x360 < 768: no upscaling
		if fr.Width != 640 || fr.Height != 360 {
			t.Errorf("frame %d dimensions %dx%d, want 640x360(no upscaling)", i, fr.Width, fr.Height)
		}
		if fr.Planned != wantTimes[i] || fr.Actual != wantTimes[i] {
			t.Errorf("frame %d time point planned=%v actual=%v, want %v", i, fr.Planned, fr.Actual, wantTimes[i])
		}
	}
}

func TestIntegrationExtractPortrait4K(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "port.mp4")
	genVideo(t, genVideoOptions{path: video, size: "2160x3840", d: 2 * time.Second, extra: []string{}})

	prober := &Prober{FFprobePath: lookTool(t, "ffprobe")}
	st, _ := domain.StatSnapshot(video)
	job := domain.VideoJob{Path: video, Snapshot: st}
	meta, jerr := prober.Probe(t.Context(), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.DisplayWidth != 2160 || meta.DisplayHeight != 3840 {
		t.Fatalf("display dimensions = %dx%d", meta.DisplayWidth, meta.DisplayHeight)
	}

	ex := &Extractor{FFmpegPath: lookTool(t, "ffmpeg"), Images: defaultImages()}
	s := NewSampler(loadDefaultSampling(t))
	plan, _ := s.Plan(meta.Duration, st.Size)
	set, jerr := ex.Extract(t.Context(), job, meta, plan)
	if jerr != nil {
		t.Fatal(jerr)
	}
	defer os.RemoveAll(set.TempDir)
	for i, fr := range set.Frames {
		if fr.Width != 432 || fr.Height != 768 {
			t.Errorf("portrait frame %d want 432x768, got %dx%d", i, fr.Width, fr.Height)
		}
	}
}

func TestIntegrationExtractSAR(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "sar.mp4")
	// 640x360 + SAR 2:1 → display 1280x360, scaled to 768x216
	genVideo(t, genVideoOptions{path: video, size: "640x360", d: 3 * time.Second, extra: []string{"-vf", "setsar=2/1"}})

	prober := &Prober{FFprobePath: lookTool(t, "ffprobe")}
	st, _ := domain.StatSnapshot(video)
	job := domain.VideoJob{Path: video, Snapshot: st}
	meta, jerr := prober.Probe(t.Context(), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.SARNum != 2 || meta.SARDen != 1 {
		t.Fatalf("SAR = %d:%d, want 2:1", meta.SARNum, meta.SARDen)
	}
	if meta.DisplayWidth != 1280 || meta.DisplayHeight != 360 {
		t.Fatalf("display dimensions = %dx%d, want 1280x360", meta.DisplayWidth, meta.DisplayHeight)
	}

	ex := &Extractor{FFmpegPath: lookTool(t, "ffmpeg"), Images: defaultImages()}
	s := NewSampler(loadDefaultSampling(t))
	plan, _ := s.Plan(meta.Duration, st.Size)
	set, jerr := ex.Extract(t.Context(), job, meta, plan)
	if jerr != nil {
		t.Fatalf("SAR extraction failed: %v (meta=%+v)", jerr, meta)
	}
	defer os.RemoveAll(set.TempDir)
	for i, fr := range set.Frames {
		if fr.Width != 768 || fr.Height != 216 {
			t.Errorf("SAR frame %d want 768x216, got %dx%d", i, fr.Width, fr.Height)
		}
	}
}

func TestIntegrationVeryShortVideo(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "tiny.mp4")
	genVideo(t, genVideoOptions{path: video, size: "320x180", d: 400 * time.Millisecond, extra: []string{}})

	prober := &Prober{FFprobePath: lookTool(t, "ffprobe")}
	st, _ := domain.StatSnapshot(video)
	job := domain.VideoJob{Path: video, Snapshot: st}
	meta, jerr := prober.Probe(t.Context(), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	s := NewSampler(loadDefaultSampling(t))
	plan, _ := s.Plan(meta.Duration, st.Size)
	if plan.TargetN != 1 {
		t.Fatalf("0.4-second video should yield one frame: %d", plan.TargetN)
	}
	ex := &Extractor{FFmpegPath: lookTool(t, "ffmpeg"), Images: defaultImages()}
	set, jerr := ex.Extract(t.Context(), job, meta, plan)
	if jerr != nil {
		t.Fatalf("short video should yield at least one frame: %v", jerr)
	}
	defer os.RemoveAll(set.TempDir)
	if len(set.Frames) != 1 {
		t.Errorf("frame count = %d", len(set.Frames))
	}
}

func TestIntegrationCorruptFile(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "broken.mp4")
	os.WriteFile(video, []byte("this is not a video file at all"), 0o644)
	prober := &Prober{FFprobePath: lookTool(t, "ffprobe")}
	st, _ := domain.StatSnapshot(video)
	job := domain.VideoJob{Path: video, Snapshot: st}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil {
		t.Fatal("corrupt file should fail probing")
	}
	t.Logf("error code: %s", jerr.Code)
}

func defaultImages() config.ImagesConfig {
	return config.ImagesConfig{MaxDimension: 768, JPEGQuality: 4}
}
