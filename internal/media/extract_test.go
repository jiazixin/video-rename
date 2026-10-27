package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

// makeJPEG writes a real JPEG file as the fake ffmpeg output fixture.
func makeJPEG(t *testing.T, path string, w, h int, quality int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
}

// fakeFFmpeg writes a fake ffmpeg script.
// copyFrom is the JPEG copied to the output argument; failAt makes a matching -ss value fail (may be empty);
// when reFrom is set, outputs ending in .re.jpg (re-encode calls) copy that file.
func fakeFFmpeg(t *testing.T, name, copyFrom, failAt string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("无 sh 可用")
	}
	body := "#!" + sh + "\n"
	if failAt != "" {
		body += `prev=""
for a in "$@"; do
  if [ "$prev" = "-ss" ] && [ "$a" = "` + failAt + `" ]; then
    echo "seek error simulated" >&2
    exit 1
  fi
  prev="$a"
done
`
	}
	body += `for last; do :; done
case "$last" in
  *.re.jpg) cp "` + copyFrom + `.re" "$last" ;;
  *) cp "` + copyFrom + `" "$last" ;;
esac
exit 0
`
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func testPlan(n int) domain.FramePlan {
	// window [2s, 8s], L=6s; all N points are segment midpoints
	plan := domain.FramePlan{
		Duration:    10 * time.Second,
		BaseN:       n,
		TargetN:     n,
		WindowStart: 2 * time.Second,
		WindowLen:   6 * time.Second,
		Times:       make([]time.Duration, n),
	}
	for i := range n {
		plan.Times[i] = plan.WindowStart + time.Duration((float64(i)+0.5)*float64(plan.WindowLen)/float64(n))
	}
	return plan
}

func testExtractor(ffmpeg string, maxDim, quality int) *Extractor {
	return &Extractor{
		FFmpegPath: ffmpeg,
		Images:     config.ImagesConfig{MaxDimension: maxDim, JPEGQuality: quality},
	}
}

func TestTargetDims(t *testing.T) {
	cases := []struct {
		w, h, max int
		tw, th    int
	}{
		{3840, 2160, 768, 768, 432},
		{2160, 3840, 768, 432, 768},
		{720, 480, 768, 720, 480},  // small images are not upscaled
		{719, 479, 768, 718, 478},  // rounded to even values
		{1920, 1080, 128, 128, 72}, // tiny maxDim
	}
	for _, tc := range cases {
		tw, th, err := targetDims(tc.w, tc.h, tc.max)
		if err != nil {
			t.Fatalf("%dx%d: %v", tc.w, tc.h, err)
		}
		if tw != tc.tw || th != tc.th {
			t.Errorf("%dx%d max=%d: got %dx%d, want %dx%d", tc.w, tc.h, tc.max, tw, th, tc.tw, tc.th)
		}
	}
	if _, _, err := targetDims(1, 100, 768); err == nil {
		t.Error("病态尺寸应拒绝")
	}
}

func TestExtractHappyPath(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "f.jpg")
	makeJPEG(t, fixture, 768, 432, 90)
	ff := fakeFFmpeg(t, "ffmpeg-ok", fixture, "")

	job, _ := fakeJob(t, 100)
	meta := domain.VideoMeta{StreamIndex: 0, DisplayWidth: 3840, DisplayHeight: 2160}
	ex := testExtractor(ff, 768, 4)
	set, jerr := ex.Extract(t.Context(), job, meta, testPlan(3))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if len(set.Frames) != 3 {
		t.Fatalf("应提取 3 帧: %d", len(set.Frames))
	}
	for i, fr := range set.Frames {
		if fr.Width != 768 || fr.Height != 432 {
			t.Errorf("帧 %d 尺寸 %dx%d", i, fr.Width, fr.Height)
		}
		if fr.Bytes == 0 || fr.Path == "" {
			t.Errorf("帧 %d 信息不完整: %+v", i, fr)
		}
		if fr.Planned != set.Plan.Times[i] {
			t.Errorf("帧 %d 计划点 %v != %v", i, fr.Planned, set.Plan.Times[i])
		}
	}
	// the temp directory must exist until the caller removes it
	if set.TempDir == "" {
		t.Fatal("TempDir 应返回给调用方")
	}
	if _, err := os.Stat(set.TempDir); err != nil {
		t.Fatalf("临时目录应仍存在: %v", err)
	}
	os.RemoveAll(set.TempDir)
}

func TestExtractRetriesAtOneThird(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "f.jpg")
	makeJPEG(t, fixture, 320, 180, 90)
	plan := testPlan(2)
	// the first midpoint 3.500 fails; the retry point is 2+1/3*3 = 3.000 (1/3 of segment [2,5))
	mid := formatSeconds(plan.Times[0])
	if mid != "3.500" {
		t.Fatalf("测试假设中点为 3.500, got %s", mid)
	}
	ff := fakeFFmpeg(t, "ffmpeg-retry", fixture, mid)

	job, _ := fakeJob(t, 100)
	meta := domain.VideoMeta{StreamIndex: 0, DisplayWidth: 320, DisplayHeight: 180}
	ex := testExtractor(ff, 768, 4)
	set, jerr := ex.Extract(t.Context(), job, meta, plan)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if len(set.Frames) != 2 || len(set.Failed) != 0 {
		t.Fatalf("补试后应全部成功: frames=%d failed=%d", len(set.Frames), len(set.Failed))
	}
	if set.Frames[0].Actual == set.Frames[0].Planned {
		t.Errorf("第一帧应记录补试点: planned=%v actual=%v", set.Frames[0].Planned, set.Frames[0].Actual)
	}
	seg := plan.WindowLen / time.Duration(2)
	retryWant := plan.WindowStart + time.Duration((1.0/3.0)*float64(seg))
	if set.Frames[0].Actual != retryWant {
		t.Errorf("补试点 = %v, 期望 %v", set.Frames[0].Actual, retryWant)
	}
	if set.Frames[1].Actual != set.Frames[1].Planned {
		t.Errorf("第二帧无需补试")
	}
	os.RemoveAll(set.TempDir)
}

func TestExtractOversizeRecompress(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.jpg")
	small := filepath.Join(dir, "big.jpg.re") // copied when a re-encode call writes .re.jpg
	makeJPEG(t, big, 320, 180, 100)
	makeJPEG(t, small, 240, 134, 50) // 320*0.75=240, 180*0.75=135→even 134
	ff := fakeFFmpeg(t, "ffmpeg-big", big, "")

	job, _ := fakeJob(t, 100)
	meta := domain.VideoMeta{StreamIndex: 0, DisplayWidth: 320, DisplayHeight: 180}
	ex := testExtractor(ff, 768, 4)
	fi, _ := os.Stat(big)
	si, _ := os.Stat(small)
	ex.ImageLimit = fi.Size() - 1 // big is over the limit, small is within it
	if ex.ImageLimit <= si.Size() {
		t.Fatalf("夹具尺寸不满足测试假设: big=%d small=%d", fi.Size(), si.Size())
	}
	set, jerr := ex.Extract(t.Context(), job, meta, testPlan(1))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if len(set.Frames) != 1 {
		t.Fatalf("应有一帧: %d (failed=%+v)", len(set.Frames), set.Failed)
	}
	fr := set.Frames[0]
	if !fr.Recompressed {
		t.Error("应标记 Recompressed")
	}
	if fr.Width != 240 || fr.Height != 134 {
		t.Errorf("重编码尺寸 %dx%d, 期望 240x134", fr.Width, fr.Height)
	}
	os.RemoveAll(set.TempDir)
}

func TestExtractImageTooLargeFailsVideo(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.jpg")
	makeJPEG(t, big, 320, 180, 100)
	ff := fakeFFmpeg(t, "ffmpeg-huge", big, "")

	job, _ := fakeJob(t, 100)
	meta := domain.VideoMeta{StreamIndex: 0, DisplayWidth: 320, DisplayHeight: 180}
	ex := testExtractor(ff, 768, 4)
	ex.ImageLimit = 10 // every image is over the limit
	set, jerr := ex.Extract(t.Context(), job, meta, testPlan(1))
	if jerr == nil || jerr.Code != domain.CodeExtractInsufficientFrames {
		t.Fatalf("应报 extract_insufficient_frames, got %+v", jerr)
	}
	if len(set.Frames) != 0 && set.TempDir != "" {
		t.Log("失败时应清理临时目录")
	}
	if set.TempDir != "" {
		if _, err := os.Stat(set.TempDir); !errors.Is(err, fs.ErrNotExist) {
			t.Error("失败后临时目录应被清理")
		}
	}
}

func TestExtractBudgetTimeout(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("无 sh")
	}
	p := filepath.Join(t.TempDir(), "ffmpeg-slow")
	os.WriteFile(p, []byte("#!"+sh+"\nsleep 5\n"), 0o755)
	job, _ := fakeJob(t, 100)
	meta := domain.VideoMeta{StreamIndex: 0, DisplayWidth: 320, DisplayHeight: 180}
	ex := testExtractor(p, 768, 4)
	ex.Budget = 150 * time.Millisecond
	set, jerr := ex.Extract(t.Context(), job, meta, testPlan(1))
	if jerr == nil || jerr.Code != domain.CodeExtractTimeout {
		t.Fatalf("应报 extract_timeout, got %+v", jerr)
	}
	if len(set.Frames) != 0 {
		t.Error("不应有成功帧")
	}
}

func TestExtractPartialFailureStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "f.jpg")
	makeJPEG(t, fixture, 320, 180, 90)
	plan := testPlan(2)
	// both midpoints and both retry points fail (retry points differ from midpoints, so every -ss fails here)
	sh, _ := exec.LookPath("sh")
	p := filepath.Join(dir, "ffmpeg-fail")
	os.WriteFile(p, []byte("#!"+sh+"\necho 'cannot seek' >&2\nexit 4\n"), 0o755)

	job, _ := fakeJob(t, 100)
	meta := domain.VideoMeta{StreamIndex: 0, DisplayWidth: 320, DisplayHeight: 180}
	ex := testExtractor(p, 768, 4)
	set, jerr := ex.Extract(t.Context(), job, meta, plan)
	if jerr == nil || jerr.Code != domain.CodeExtractInsufficientFrames {
		t.Fatalf("零帧应失败, got %+v", jerr)
	}
	if len(set.Failed) != 2 {
		t.Errorf("应记录 2 个失败点: %d", len(set.Failed))
	}
	_ = fixture
}

func TestExtractArgumentsIncludeSeekAndMap(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "f.jpg")
	makeJPEG(t, fixture, 320, 180, 90)
	argsFile := filepath.Join(dir, "args.txt")
	sh, _ := exec.LookPath("sh")
	p := filepath.Join(dir, "ffmpeg-args")
	os.WriteFile(p, []byte("#!"+sh+"\necho \"$@\" > \""+argsFile+"\"\nfor last; do :; done\ncp \""+fixture+"\" \"$last\"\n"), 0o755)

	job, path := fakeJob(t, 100)
	meta := domain.VideoMeta{StreamIndex: 3, DisplayWidth: 320, DisplayHeight: 180}
	ex := testExtractor(p, 768, 7)
	set, jerr := ex.Extract(t.Context(), job, meta, testPlan(1))
	if jerr != nil {
		t.Fatal(jerr)
	}
	raw, _ := os.ReadFile(argsFile)
	args := strings.Fields(string(raw))
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{
		" -nostdin ", " -hide_banner ", " -loglevel error ",
		" -threads 2 ", " -ss 5.000 ", " -i " + path + " ",
		" -map 0:3 ", " -frames:v 1 ", " -filter_threads 1 ",
		" -vf scale=320:180:flags=lanczos,setsar=1 ", " -q:v 7 ", " -update 1 ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ffmpeg 参数缺少 %s", want)
		}
	}
	os.RemoveAll(set.TempDir)
}

func TestExtractEmptyPlan(t *testing.T) {
	job, _ := fakeJob(t, 100)
	ex := testExtractor("ffmpeg", 768, 4)
	set, jerr := ex.Extract(t.Context(), job, domain.VideoMeta{DisplayWidth: 320, DisplayHeight: 180}, domain.FramePlan{})
	if jerr == nil || jerr.Code != domain.CodeExtractInsufficientFrames {
		t.Fatalf("空计划应失败, got %+v", jerr)
	}
	_ = set
}

func TestExtractMissingResolvedBinaryStopsBatch(t *testing.T) {
	path := writeScript(t, "exit 0\n")
	resolved, err := exec.LookPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	job, _ := fakeJob(t, 100)
	ex := testExtractor(resolved, 768, 4)
	_, jerr := ex.Extract(t.Context(), job, domain.VideoMeta{DisplayWidth: 320, DisplayHeight: 180}, testPlan(1))
	if jerr == nil || jerr.Code != domain.CodeToolNotFound || !jerr.Global {
		t.Fatalf("deleted resolved ffmpeg must stop the batch: %+v", jerr)
	}
}

func TestExtractToolVanishingBeforeRetryStopsBatch(t *testing.T) {
	// The first failed attempt deletes its own executable. Even for a single
	// sampling point, the failed retry must retain the global dependency error.
	path := writeScript(t, "rm -- \"$0\"\nexit 1\n")
	job, _ := fakeJob(t, 100)
	ex := testExtractor(path, 768, 4)
	_, jerr := ex.Extract(t.Context(), job, domain.VideoMeta{DisplayWidth: 320, DisplayHeight: 180}, testPlan(1))
	if jerr == nil || jerr.Code != domain.CodeToolNotFound || !jerr.Global {
		t.Fatalf("ffmpeg disappearing before retry must stop the batch: %+v", jerr)
	}
}

func TestExtractCancelledContext(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "f.jpg")
	makeJPEG(t, fixture, 320, 180, 90)
	ff := fakeFFmpeg(t, "ffmpeg-cancel", fixture, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	job, _ := fakeJob(t, 100)
	meta := domain.VideoMeta{DisplayWidth: 320, DisplayHeight: 180}
	ex := testExtractor(ff, 768, 4)
	_, jerr := ex.Extract(ctx, job, meta, testPlan(1))
	if jerr == nil {
		t.Fatal("已取消的 context 应失败")
	}
}

func TestFormatSeconds(t *testing.T) {
	if got := formatSeconds(0); got != "0.000" {
		t.Errorf("got %s", got)
	}
	if got := formatSeconds(12600 * time.Millisecond); got != "12.600" {
		t.Errorf("got %s", got)
	}
}

var _ = fmt.Sprintf

func TestExtractKeepsBothAttemptDiagnostics(t *testing.T) {
	dir := t.TempDir()
	ff := filepath.Join(dir, "ffmpeg-fail")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\necho 'decoder unavailable' >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	job, _ := fakeJob(t, 100)
	ex := testExtractor(ff, 768, 4)
	_, err := ex.Extract(t.Context(), job, domain.VideoMeta{StreamIndex: 0, DisplayWidth: 320, DisplayHeight: 180}, testPlan(2))
	if err == nil {
		t.Fatal("expected extraction failure")
	}
	for _, want := range []string{"first attempt", "retry", "decoder unavailable", "exit code 7", "planned frames: 2"} {
		if !strings.Contains(err.ProcessingLog, want) {
			t.Fatalf("missing %s: %s", want, err.ProcessingLog)
		}
	}
}
