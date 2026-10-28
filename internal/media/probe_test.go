package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/domain"
)

func loadProbeFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "probe", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fakeJob(t *testing.T, size int64) (domain.VideoJob, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	return domain.VideoJob{Path: p, Snapshot: snap}, p
}

func TestParseNormalSelectsDefaultVideoStream(t *testing.T) {
	job, _ := fakeJob(t, 1000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "normal.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.StreamIndex != 0 {
		t.Errorf("stream index = %d, want 0(skip audio)", meta.StreamIndex)
	}
	if meta.Duration != 120500*time.Millisecond {
		t.Errorf("duration = %v", meta.Duration)
	}
	if meta.DisplayWidth != 1920 || meta.DisplayHeight != 1080 {
		t.Errorf("display dimensions = %dx%d", meta.DisplayWidth, meta.DisplayHeight)
	}
	if meta.HDR {
		t.Error("SDR video must not be marked HDR")
	}
}

func TestParseCoverSkipped(t *testing.T) {
	job, _ := fakeJob(t, 2000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "cover.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.StreamIndex != 1 {
		t.Errorf("cover stream at index 0 must not be selected; got = %d", meta.StreamIndex)
	}
}

func TestParseDurationFallbackToFormat(t *testing.T) {
	job, _ := fakeJob(t, 3000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "no_stream_duration.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.Duration != 45250*time.Millisecond {
		t.Errorf("should fall back to format.duration, got %v", meta.Duration)
	}
	// SAR 0:1 means square pixels
	if meta.SARNum != 1 || meta.SARDen != 1 {
		t.Errorf("SAR 0:1 should be treated as 1:1")
	}
}

func TestParseRotationAndSAR(t *testing.T) {
	job, _ := fakeJob(t, 4000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "rotated_sar.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	// 720×480, SAR 8:9 → 640×480; rotation -90 (=270) → 480×640
	if meta.DisplayWidth != 480 || meta.DisplayHeight != 640 {
		t.Errorf("display dimensions = %dx%d, want 480x640", meta.DisplayWidth, meta.DisplayHeight)
	}
	if meta.Rotation != 270 {
		t.Errorf("rotation = %d, want 270", meta.Rotation)
	}
}

func TestParseAudioOnly(t *testing.T) {
	job, _ := fakeJob(t, 5000)
	_, jerr := parseProbeOutput(loadProbeFixture(t, "audio_only.json"), job)
	if jerr == nil || jerr.Code != domain.CodeProbeNoVideo {
		t.Fatalf("audio-only input should return probe_no_video, got %+v", jerr)
	}
}

func TestParseHDRFlag(t *testing.T) {
	job, _ := fakeJob(t, 6000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "hdr.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if !meta.HDR || !meta.HDRNotToneMap {
		t.Error("smpte2084 should mark HDR")
	}
}

func TestParseUnknownDuration(t *testing.T) {
	data := `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"sample_aspect_ratio":"1:1","disposition":{"default":1}}],"format":{"size":"1"}}`
	job, _ := fakeJob(t, 1)
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil || jerr.Code != domain.CodeProbeDurationUnknown {
		t.Fatalf("missing duration should return probe_duration_unknown, got %+v", jerr)
	}
	// negative durations are rejected too
	data = `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"duration":"-5","sample_aspect_ratio":"1:1","disposition":{"default":1}}],"format":{"duration":"-5"}}`
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil || jerr.Code != domain.CodeProbeDurationUnknown {
		t.Fatalf("negative duration should fail, got %+v", jerr)
	}
}

func TestParseBadSARRejected(t *testing.T) {
	data := `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"duration":"10","sample_aspect_ratio":"bogus","disposition":{"default":1}}],"format":{"duration":"10"}}`
	job, _ := fakeJob(t, 1)
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil || jerr.Code != domain.CodeProbeJSONInvalid {
		t.Fatalf("invalid SAR should fail, got %+v", jerr)
	}
}

func TestParseBadRotationRejected(t *testing.T) {
	data := `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"duration":"10","sample_aspect_ratio":"1:1","side_data_list":[{"side_data_type":"Display Matrix","rotation":45.0}],"disposition":{"default":1}}],"format":{"duration":"10"}}`
	job, _ := fakeJob(t, 1)
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil {
		t.Fatal("45-degree rotation should fail")
	}
}

func TestParseSizeMismatchUnchangedContinues(t *testing.T) {
	job, path := fakeJob(t, 1000)
	// ffprobe reports size=999, unlike the snapshot, but the file is unchanged → continue with stat
	data := strings.Replace(string(loadProbeFixture(t, "normal.json")), `"size": "1000"`, `"size": "999"`, 1)
	meta, jerr := parseProbeOutput([]byte(data), job)
	if jerr != nil {
		t.Fatalf("unchanged file should proceed: %v", jerr)
	}
	if meta.SizeBytes != 1000 {
		t.Errorf("want Go stat size, got %d", meta.SizeBytes)
	}
	_ = path
}

func TestParseSizeMismatchChangedReportsSourceChanged(t *testing.T) {
	job, path := fakeJob(t, 1000)
	// parse again after changing the file size and mtime → source_changed
	if err := os.WriteFile(path, make([]byte, 2000), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	data := strings.Replace(string(loadProbeFixture(t, "normal.json")), `"size": "1000"`, `"size": "2000"`, 1)
	_, jerr := parseProbeOutput([]byte(data), job)
	if jerr == nil || jerr.Code != domain.CodeSourceChanged {
		t.Fatalf("changed file should return source_changed, got %+v", jerr)
	}
}

// writeScript writes an executable shell script as a fake ffprobe.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable")
	}
	p := filepath.Join(t.TempDir(), "fake-ffprobe")
	if err := os.WriteFile(p, []byte("#!"+sh+"\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProbeExecFailure(t *testing.T) {
	script := writeScript(t, "echo 'Invalid data found when processing input' >&2\nexit 1\n")
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: script}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeExecFailed {
		t.Fatalf("execution failure should return probe_exec_failed, got %+v", jerr)
	}
	if jerr.ExitCode != 1 {
		t.Errorf("exit code = %d", jerr.ExitCode)
	}
	if !strings.Contains(jerr.StderrTail, "Invalid data") {
		t.Errorf("stderr tail should be retained: %q", jerr.StderrTail)
	}
}

func TestProbeTimeout(t *testing.T) {
	script := writeScript(t, "sleep 2\n")
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: script, Timeout: 150 * time.Millisecond}
	start := time.Now()
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeTimeout {
		t.Fatalf("want probe_timeout, got %+v", jerr)
	}
	if time.Since(start) > time.Second {
		t.Error("timeout should return promptly")
	}
}

func TestProbeOutputLimit(t *testing.T) {
	script := writeScript(t, "yes '{\"streams\":[]}' | head -c 100000\n")
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: script, StdoutLimit: 1024}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeOutputLimit {
		t.Fatalf("want probe_output_limit, got %+v", jerr)
	}
}

func TestProbeBadJSON(t *testing.T) {
	script := writeScript(t, "echo 'not-json'\n")
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: script}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeJSONInvalid {
		t.Fatalf("want probe_json_invalid, got %+v", jerr)
	}
}

func TestProbeMissingBinary(t *testing.T) {
	job, _ := fakeJob(t, 10)
	path := writeScript(t, "exit 0\n")
	resolved, err := exec.LookPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	prober := &Prober{FFprobePath: resolved}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeToolNotFound || !jerr.Global {
		t.Fatalf("missing probe tool should stop the batch, got %+v", jerr)
	}
}

func TestProbeScriptOutputsFixture(t *testing.T) {
	// fake ffprobe output fixture
	fixture := string(loadProbeFixture(t, "normal.json"))
	script := writeScript(t, "cat <<'JSONEOF'\n"+fixture+"\nJSONEOF\n")
	job, _ := fakeJob(t, 1000)
	prober := &Prober{FFprobePath: script}
	meta, jerr := prober.Probe(t.Context(), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.Duration != 120500*time.Millisecond {
		t.Errorf("unexpected end-to-end parse result: %+v", meta)
	}
}
