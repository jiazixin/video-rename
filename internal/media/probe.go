// Package media 封装 ffprobe 探测、采样计划与 FFmpeg JPEG 抽帧。
// 媒体模块不访问 HTTP;所有外部命令通过参数数组启动,不拼接 shell 命令。
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"video-rename/internal/domain"
)

// ffprobeOutput 与 ffprobe JSON 输出的相关子集。
type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
		Size     string `json:"size"`
	} `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

type ffprobeStream struct {
	Index             int    `json:"index"`
	CodecType         string `json:"codec_type"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	Duration          string `json:"duration"`
	SampleAspectRatio string `json:"sample_aspect_ratio"`
	PixFmt            string `json:"pix_fmt"`
	ColorTransfer     string `json:"color_transfer"`
	ColorPrimaries    string `json:"color_primaries"`
	Disposition       struct {
		Default     int `json:"default"`
		AttachedPic int `json:"attached_pic"`
	} `json:"disposition"`
	SideDataList []struct {
		SideDataType string   `json:"side_data_type"`
		Rotation     *float64 `json:"rotation"`
	} `json:"side_data_list"`
	Tags map[string]string `json:"tags"`
}

// Prober 执行 ffprobe 探测。所有上限可注入以便测试;零值取内置默认。
type Prober struct {
	FFprobePath string
	Timeout     time.Duration // 探测超时,默认 30s
	StdoutLimit int64         // stdout 上限,默认 4 MiB
	StderrTail  int           // stderr 保留量,默认 8 KiB
}

func (p *Prober) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return domain.DefaultBudgets().ProbeTimeout
}

func (p *Prober) stdoutLimit() int64 {
	if p.StdoutLimit > 0 {
		return p.StdoutLimit
	}
	return domain.DefaultBudgets().ProbeStdoutLimit
}

// limitedWriter 接受至多 max 字节,超出后停止记录并标记溢出;
// 写入方(进程管道)获得错误从而使子进程被终止。
type limitedWriter struct {
	buf      bytes.Buffer
	max      int64
	overflow bool
}

func (w *limitedWriter) Write(b []byte) (int, error) {
	if w.overflow {
		return 0, errors.New("output limit exceeded")
	}
	room := int(w.max) - w.buf.Len()
	if room < len(b) {
		w.overflow = true
		if room > 0 {
			w.buf.Write(b[:room])
		}
		return len(b), nil // 丢弃剩余,下次写入报错
	}
	w.buf.Write(b)
	return len(b), nil
}

// Probe 探测单个视频。
// 错误以 *domain.JobError 返回,区分执行失败、超时、输出超限、JSON 无效、
// 无视频流与时长未知;提交前的大小核验失败报 SOURCE_CHANGED。
func (p *Prober) Probe(ctx context.Context, job domain.VideoJob) (domain.VideoMeta, *domain.JobError) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, p.FFprobePath,
		"-v", "error", "-show_format", "-show_streams", "-of", "json", job.Path)
	// 取消后若孤儿子进程仍持有管道,WaitDelay 强制回收,避免等待悬挂
	cmd.WaitDelay = 500 * time.Millisecond
	var stdout limitedWriter
	stdout.max = p.stdoutLimit()
	var stderr limitedWriter
	stderr.max = 64 * 1024
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	tail := tailBytes(stderr.buf.Bytes(), domain.DefaultBudgets().StderrTailLimit)
	if stdout.overflow {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeOutputLimit,
			Message: fmt.Sprintf("ffprobe stdout 超过 %d 字节上限", p.stdoutLimit()),
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return domain.VideoMeta{}, &domain.JobError{
			Code:       domain.CodeProbeTimeout,
			Message:    "ffprobe 探测超时",
			StderrTail: string(tail),
		}
	}
	if ctx.Err() == context.Canceled {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeCancelled,
			Message: "探测被取消",
		}
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		exitCode := 0
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		je := &domain.JobError{
			Code:       domain.CodeProbeExecFailed,
			Message:    fmt.Sprintf("ffprobe 执行失败: %v", runErr),
			ExitCode:   exitCode,
			StderrTail: string(tail),
		}
		if errors.Is(runErr, exec.ErrNotFound) {
			je.Code = domain.CodeToolNotFound
			je.Message = "ffprobe 在运行中消失(PATH 中不再存在)"
			je.Global = true
		}
		return domain.VideoMeta{}, je
	}
	meta, jerr := parseProbeOutput(stdout.buf.Bytes(), job)
	if jerr != nil {
		jerr.StderrTail = string(tail)
		return domain.VideoMeta{}, jerr
	}
	return meta, nil
}

// parseProbeOutput 解析 ffprobe JSON 并完成流选择与派生计算。
func parseProbeOutput(data []byte, job domain.VideoJob) (domain.VideoMeta, *domain.JobError) {
	var out ffprobeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeJSONInvalid,
			Message: fmt.Sprintf("ffprobe JSON 解析失败: %v", err),
		}
	}
	// 流选择:首个 default 且非封面的视频流;否则第一个非封面视频流。
	var chosen *ffprobeStream
	var firstNonCover *ffprobeStream
	for i := range out.Streams {
		s := &out.Streams[i]
		if s.CodecType != "video" {
			continue
		}
		if s.Disposition.AttachedPic == 1 {
			continue
		}
		if firstNonCover == nil {
			firstNonCover = s
		}
		if s.Disposition.Default == 1 && chosen == nil {
			chosen = s
		}
	}
	if chosen == nil {
		chosen = firstNonCover
	}
	if chosen == nil {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeNoVideo,
			Message: "没有可用的视频流(纯音频、仅封面或无视频轨)",
		}
	}

	// 时长:优先所选视频流 duration,缺失回退 format.duration。
	duration, ok := parseFiniteSeconds(chosen.Duration)
	if !ok {
		duration, ok = parseFiniteSeconds(out.Format.Duration)
	}
	if !ok || duration <= 0 {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeDurationUnknown,
			Message: "无法获得有限且大于 0 的时长(流与 format 均缺失或无效)",
		}
	}

	meta := domain.VideoMeta{
		StreamIndex: chosen.Index,
		Duration:    time.Duration(duration * float64(time.Second)),
		SizeBytes:   job.Snapshot.Size,
		Width:       chosen.Width,
		Height:      chosen.Height,
	}

	// SAR:缺失、"N/A" 与 "0:1" 按方形像素;其余必须可解释。
	sarNum, sarDen := 1, 1
	sar := strings.TrimSpace(chosen.SampleAspectRatio)
	if sar != "" && sar != "N/A" && sar != "0:1" && sar != "0" {
		n, d, err := parseRatio(sar)
		if err != nil || n < 0 || d <= 0 {
			return domain.VideoMeta{}, &domain.JobError{
				Code:    domain.CodeProbeJSONInvalid,
				Message: fmt.Sprintf("无法解释的 sample_aspect_ratio %q", chosen.SampleAspectRatio),
			}
		}
		sarNum, sarDen = n, d
	}
	meta.SARNum, meta.SARDen = sarNum, sarDen

	// 旋转:优先 side_data_list 的 display matrix,回退 tags.rotate。
	rotDeg := 0.0
	for _, sd := range chosen.SideDataList {
		if sd.SideDataType == "Display Matrix" && sd.Rotation != nil {
			rotDeg = *sd.Rotation
		}
	}
	if rotDeg == 0 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(chosen.Tags["rotate"]), 64); err == nil {
			rotDeg = v
		}
	}
	rot := ((int(math.Round(rotDeg)) % 360) + 360) % 360
	if rot%90 != 0 {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeJSONInvalid,
			Message: fmt.Sprintf("无法解释的旋转元信息 %v 度", rotDeg),
		}
	}
	meta.Rotation = rot

	// 显示尺寸 = 编码尺寸 × SAR,90/270 度旋转后交换宽高。
	dw := float64(chosen.Width) * float64(sarNum) / float64(sarDen)
	dh := float64(chosen.Height)
	if rot == 90 || rot == 270 {
		meta.DisplayWidth, meta.DisplayHeight = int(math.Round(dh)), int(math.Round(dw))
	} else {
		meta.DisplayWidth, meta.DisplayHeight = int(math.Round(dw)), int(math.Round(dh))
	}
	if meta.DisplayWidth <= 0 || meta.DisplayHeight <= 0 {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeJSONInvalid,
			Message: "视频流编码尺寸无效",
		}
	}

	// HDR 传递特性检测(仅用于 warning,不做色调映射)。
	switch chosen.ColorTransfer {
	case "smpte2084", "arib-std-b67":
		meta.HDR = true
		meta.HDRNotToneMap = true
	}

	// 大小核验:与 format.size 明显不一致时重新核验文件是否变化。
	if fs, err := strconv.ParseInt(strings.TrimSpace(out.Format.Size), 10, 64); err == nil && fs >= 0 && fs != job.Snapshot.Size {
		cur, statErr := domain.StatSnapshot(job.Path)
		switch {
		case statErr != nil:
			return domain.VideoMeta{}, &domain.JobError{
				Code:    domain.CodeSourceChanged,
				Message: fmt.Sprintf("重新核验文件失败: %v", statErr),
			}
		case !cur.Same(job.Snapshot):
			return domain.VideoMeta{}, &domain.JobError{
				Code:    domain.CodeSourceChanged,
				Message: "文件在探测期间被外部修改",
			}
		}
		// 身份未变但 ffprobe 报告不同大小:以 Go stat 为准,继续处理。
	}
	return meta, nil
}

// parseFiniteSeconds 解析 ffprobe 的秒字符串,要求有限且可解析。
func parseFiniteSeconds(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// parseRatio 解析 "num:den"。
func parseRatio(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid ratio %q", s)
	}
	n, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	d, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("invalid ratio %q", s)
	}
	return n, d, nil
}

// tailBytes 返回末尾至多 n 字节。
func tailBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}
