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
		t.Errorf("流索引 = %d, 期望 0(跳过音频)", meta.StreamIndex)
	}
	if meta.Duration != 120500*time.Millisecond {
		t.Errorf("时长 = %v", meta.Duration)
	}
	if meta.DisplayWidth != 1920 || meta.DisplayHeight != 1080 {
		t.Errorf("显示尺寸 = %dx%d", meta.DisplayWidth, meta.DisplayHeight)
	}
	if meta.HDR {
		t.Error("SDR 视频不应标记 HDR")
	}
}

func TestParseCoverSkipped(t *testing.T) {
	job, _ := fakeJob(t, 2000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "cover.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.StreamIndex != 1 {
		t.Errorf("封面流(index 0)不应被选中,选中 = %d", meta.StreamIndex)
	}
}

func TestParseDurationFallbackToFormat(t *testing.T) {
	job, _ := fakeJob(t, 3000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "no_stream_duration.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.Duration != 45250*time.Millisecond {
		t.Errorf("应回退 format.duration, got %v", meta.Duration)
	}
	// SAR 0:1 按方形像素
	if meta.SARNum != 1 || meta.SARDen != 1 {
		t.Errorf("SAR 0:1 应按 1:1 处理")
	}
}

func TestParseRotationAndSAR(t *testing.T) {
	job, _ := fakeJob(t, 4000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "rotated_sar.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	// 720×480,SAR 8:9 → 640×480;旋转 -90(=270)→ 480×640
	if meta.DisplayWidth != 480 || meta.DisplayHeight != 640 {
		t.Errorf("显示尺寸 = %dx%d, 期望 480x640", meta.DisplayWidth, meta.DisplayHeight)
	}
	if meta.Rotation != 270 {
		t.Errorf("旋转 = %d, 期望 270", meta.Rotation)
	}
}

func TestParseAudioOnly(t *testing.T) {
	job, _ := fakeJob(t, 5000)
	_, jerr := parseProbeOutput(loadProbeFixture(t, "audio_only.json"), job)
	if jerr == nil || jerr.Code != domain.CodeProbeNoVideo {
		t.Fatalf("纯音频应报 PROBE_NO_VIDEO, got %+v", jerr)
	}
}

func TestParseHDRFlag(t *testing.T) {
	job, _ := fakeJob(t, 6000)
	meta, jerr := parseProbeOutput(loadProbeFixture(t, "hdr.json"), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if !meta.HDR || !meta.HDRNotToneMap {
		t.Error("smpte2084 应标记 HDR")
	}
}

func TestParseUnknownDuration(t *testing.T) {
	data := `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"sample_aspect_ratio":"1:1","disposition":{"default":1}}],"format":{"size":"1"}}`
	job, _ := fakeJob(t, 1)
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil || jerr.Code != domain.CodeProbeDurationUnknown {
		t.Fatalf("缺时长应报 PROBE_DURATION_UNKNOWN, got %+v", jerr)
	}
	// 负时长同样拒绝
	data = `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"duration":"-5","sample_aspect_ratio":"1:1","disposition":{"default":1}}],"format":{"duration":"-5"}}`
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil || jerr.Code != domain.CodeProbeDurationUnknown {
		t.Fatalf("负时长应报错, got %+v", jerr)
	}
}

func TestParseBadSARRejected(t *testing.T) {
	data := `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"duration":"10","sample_aspect_ratio":"bogus","disposition":{"default":1}}],"format":{"duration":"10"}}`
	job, _ := fakeJob(t, 1)
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil || jerr.Code != domain.CodeProbeJSONInvalid {
		t.Fatalf("异常 SAR 应报错, got %+v", jerr)
	}
}

func TestParseBadRotationRejected(t *testing.T) {
	data := `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"duration":"10","sample_aspect_ratio":"1:1","side_data_list":[{"side_data_type":"Display Matrix","rotation":45.0}],"disposition":{"default":1}}],"format":{"duration":"10"}}`
	job, _ := fakeJob(t, 1)
	if _, jerr := parseProbeOutput([]byte(data), job); jerr == nil {
		t.Fatal("45 度旋转应报错")
	}
}

func TestParseSizeMismatchUnchangedContinues(t *testing.T) {
	job, path := fakeJob(t, 1000)
	// ffprobe 报告 size=999 与快照不一致,但文件未变 → 继续以 stat 为准
	data := strings.Replace(string(loadProbeFixture(t, "normal.json")), `"size": "1000"`, `"size": "999"`, 1)
	meta, jerr := parseProbeOutput([]byte(data), job)
	if jerr != nil {
		t.Fatalf("文件未变时应继续: %v", jerr)
	}
	if meta.SizeBytes != 1000 {
		t.Errorf("以 Go stat 为准, got %d", meta.SizeBytes)
	}
	_ = path
}

func TestParseSizeMismatchChangedReportsSourceChanged(t *testing.T) {
	job, path := fakeJob(t, 1000)
	// 修改文件大小与 mtime 后再解析 → SOURCE_CHANGED
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
		t.Fatalf("文件已变化应报 SOURCE_CHANGED, got %+v", jerr)
	}
}

// writeScript 生成可执行 shell 脚本作为假 ffprobe。
func writeScript(t *testing.T, body string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("无 sh 可用")
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
		t.Fatalf("执行失败应报 PROBE_EXEC_FAILED, got %+v", jerr)
	}
	if jerr.ExitCode != 1 {
		t.Errorf("退出码 = %d", jerr.ExitCode)
	}
	if !strings.Contains(jerr.StderrTail, "Invalid data") {
		t.Errorf("stderr 尾部应保留: %q", jerr.StderrTail)
	}
}

func TestProbeTimeout(t *testing.T) {
	script := writeScript(t, "sleep 2\n")
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: script, Timeout: 150 * time.Millisecond}
	start := time.Now()
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeTimeout {
		t.Fatalf("应报 PROBE_TIMEOUT, got %+v", jerr)
	}
	if time.Since(start) > time.Second {
		t.Error("超时应及时返回")
	}
}

func TestProbeOutputLimit(t *testing.T) {
	script := writeScript(t, "yes '{\"streams\":[]}' | head -c 100000\n")
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: script, StdoutLimit: 1024}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeOutputLimit {
		t.Fatalf("应报 PROBE_OUTPUT_LIMIT, got %+v", jerr)
	}
}

func TestProbeBadJSON(t *testing.T) {
	script := writeScript(t, "echo 'not-json'\n")
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: script}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeJSONInvalid {
		t.Fatalf("应报 PROBE_JSON_INVALID, got %+v", jerr)
	}
}

func TestProbeMissingBinary(t *testing.T) {
	job, _ := fakeJob(t, 10)
	prober := &Prober{FFprobePath: filepath.Join(t.TempDir(), "no-such-tool")}
	_, jerr := prober.Probe(t.Context(), job)
	if jerr == nil || jerr.Code != domain.CodeProbeExecFailed {
		t.Fatalf("工具缺失应报 PROBE_EXEC_FAILED(启动前另有 TOOL_NOT_FOUND 检查), got %+v", jerr)
	}
}

func TestProbeScriptOutputsFixture(t *testing.T) {
	// 假 ffprobe 输出夹具内容
	fixture := string(loadProbeFixture(t, "normal.json"))
	script := writeScript(t, "cat <<'JSONEOF'\n"+fixture+"\nJSONEOF\n")
	job, _ := fakeJob(t, 1000)
	prober := &Prober{FFprobePath: script}
	meta, jerr := prober.Probe(t.Context(), job)
	if jerr != nil {
		t.Fatal(jerr)
	}
	if meta.Duration != 120500*time.Millisecond {
		t.Errorf("端到端解析异常: %+v", meta)
	}
}
