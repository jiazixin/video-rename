package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 解码用于逐图校验
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

// Extractor 用 FFmpeg 按计划提取 JPEG 截图。
// 每个 Extract 创建私有临时目录；失败/取消时清理，成功后由调用方接管。
type Extractor struct {
	TempRoot   string // Optional managed workspace; caller owns the parent.
	FFmpegPath string
	Images     config.ImagesConfig
	Budget     time.Duration // 抽帧总预算,默认 120s
	ImageLimit int64         // 单图字节上限,默认 1 MiB
	RunID      string        // 临时目录前缀成分
}

func (e *Extractor) budget() time.Duration {
	if e.Budget > 0 {
		return e.Budget
	}
	return domain.DefaultBudgets().ExtractBudget
}

func (e *Extractor) imageLimit() int64 {
	if e.ImageLimit > 0 {
		return e.ImageLimit
	}
	return domain.DefaultBudgets().ImageLimit
}

// targetDims 计算目标输出尺寸:
// s = min(1, max_dimension / max(W,H)),不放大小图;
// 目标宽高向下取可编码偶数,至少 2 像素;病态小输入拒绝。
func targetDims(w, h, maxDim int) (int, int, error) {
	if w < 2 || h < 2 {
		return 0, 0, fmt.Errorf("病态视频尺寸 %dx%d(小于 2 像素)", w, h)
	}
	s := float64(1)
	if m := float64(maxDim) / float64(max(w, h)); m < s {
		s = m
	}
	tw := evenFloor(float64(w) * s)
	th := evenFloor(float64(h) * s)
	return tw, th, nil
}

func evenFloor(v float64) int {
	n := int(v) // 向下取整
	if n < 2 {
		return 2
	}
	return n - n%2
}

// Extract 按计划逐点提取截图。
//
// 行为要点(docs/processing.md 第 3 节):
//   - 每点输入侧 seek,显式 map 绝对流索引,单帧输出;
//   - 单点首次失败仅补试一次,移至该分段 1/3 处;
//   - 全部尝试共享视频内置抽帧预算,预算耗尽不再尝试剩余点;
//   - 图片超 1 MiB 上限时最多重新编码一次(缩至 75%,质量 +3);
//   - 成功帧数 ≥ 1 即可继续,零帧失败(超时为主因报 EXTRACT_TIMEOUT,
//     否则 EXTRACT_INSUFFICIENT_FRAMES)。
//
// 即使返回错误,set 中也携带已完成的帧与 warnings 供日志使用。
func (e *Extractor) Extract(ctx context.Context, job domain.VideoJob, meta domain.VideoMeta, plan domain.FramePlan) (set domain.FrameSet, jobErr *domain.JobError) {
	set = domain.FrameSet{Plan: plan}
	var diagnostics strings.Builder
	fmt.Fprintf(&diagnostics, "FFmpeg: %s\n视频: %s\n视频流: %d · 尺寸: %dx%d · 计划帧数: %d · 总预算: %s\n", e.FFmpegPath, job.Path, meta.StreamIndex, meta.DisplayWidth, meta.DisplayHeight, len(plan.Times), e.budget())
	record := func(at time.Duration, attempt string, err error) {
		if err != nil && diagnostics.Len() < 120*1024 {
			fmt.Fprintf(&diagnostics, "\n采样时间 %s · %s\n%s\n", at, attempt, err)
		}
	}
	defer func() {
		if jobErr != nil {
			fmt.Fprintf(&diagnostics, "\n结果: %s\n成功帧: %d\n警告: %s\n", jobErr.Error(), len(set.Frames), strings.Join(set.Warn, "\n"))
			jobErr.ProcessingLog = diagnostics.String()
		}
	}()
	tw, th, err := targetDims(meta.DisplayWidth, meta.DisplayHeight, e.Images.MaxDimension)
	if err != nil {
		set.Warn = append(set.Warn, err.Error())
		return set, &domain.JobError{Code: domain.CodeExtractFailed, Message: err.Error()}
	}

	tmp, merr := os.MkdirTemp(e.TempRoot, "video-rename-"+e.RunID+"-")
	if merr != nil {
		return set, &domain.JobError{Code: domain.CodeExtractFailed, Message: fmt.Sprintf("创建临时目录失败: %v", merr)}
	}
	// 成功时临时目录交给调用方(截图在推理阶段仍需存在);
	// 失败或取消时在此清理。
	set.TempDir = tmp
	cleanupNow := true
	defer func() {
		if cleanupNow {
			if rmErr := os.RemoveAll(tmp); rmErr != nil {
				set.Warn = append(set.Warn, fmt.Sprintf("清理临时目录失败: %v", rmErr))
			}
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, e.budget())
	defer cancel()

	n := len(plan.Times)
	seg := time.Duration(0)
	if n > 0 {
		seg = plan.WindowLen / time.Duration(n)
	}
	for i := range n {
		if ctx.Err() != nil {
			set.Warn = append(set.Warn, fmt.Sprintf("抽帧预算耗尽,剩余 %d 个采样点未尝试", n-i))
			break
		}
		planned := plan.Times[i]
		// 补试点:该分段 1/3 处
		retry := plan.WindowStart + time.Duration((float64(i)+1.0/3.0)*float64(seg))
		out := filepath.Join(tmp, fmt.Sprintf("frame-%02d.jpg", i))

		frame, ferr := e.extractPoint(ctx, job, meta, planned, out, tw, th)
		record(planned, "首次抽帧", ferr)
		if ferr != nil && errors.Is(ferr, errToolVanished) {
			return set, &domain.JobError{
				Code: domain.CodeToolNotFound, Message: errToolVanished.Error(), Global: true,
			}
		}
		if ferr != nil && ctx.Err() == nil {
			// 首次失败:从分段中点移至 1/3 处补试一次
			out2 := filepath.Join(tmp, fmt.Sprintf("frame-%02d-r.jpg", i))
			frame, ferr = e.extractPoint(ctx, job, meta, retry, out2, tw, th)
			record(retry, "补试抽帧", ferr)
			if ferr == nil {
				frame.Planned, frame.Actual = planned, retry
			}
		}
		if ferr != nil {
			if ctx.Err() == context.Canceled {
				return set, &domain.JobError{Code: domain.CodeCancelled, Message: "抽帧被取消"}
			}
			if ctx.Err() != nil {
				set.Warn = append(set.Warn, fmt.Sprintf("抽帧预算耗尽于第 %d 点", i+1))
				break
			}
			set.Failed = append(set.Failed, domain.FailedPoint{Planned: planned, Actual: retry, Reason: ferr.Error()})
			continue
		}
		frame.Planned = planned
		if frame.Actual == 0 {
			frame.Actual = planned
		}
		set.Frames = append(set.Frames, frame)
	}

	if len(set.Frames) == 0 {
		if ctx.Err() == context.DeadlineExceeded {
			return set, &domain.JobError{Code: domain.CodeExtractTimeout, Message: "抽帧总预算耗尽且没有成功帧"}
		}
		msg := fmt.Sprintf("计划 %d 帧全部失败", n)
		if n == 0 {
			msg = "采样计划为空"
		}
		return set, &domain.JobError{Code: domain.CodeExtractInsufficientFrames, Message: msg}
	}
	cleanupNow = false // 成功:临时目录由调用方在视频完成后清理
	return set, nil
}

// extractPoint 在单个时间点执行一次 FFmpeg 抽帧并校验输出。
func (e *Extractor) extractPoint(ctx context.Context, job domain.VideoJob, meta domain.VideoMeta, at time.Duration, out string, tw, th int) (result domain.Frame, resultErr error) {
	defer func() {
		for _, p := range []string{out, out + ".re.jpg"} {
			if p != result.Path {
				_ = os.Remove(p)
			}
		}
	}()
	scale := fmt.Sprintf("scale=%d:%d:flags=lanczos,setsar=1", tw, th)
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-threads", "2", // 解码端线程
		"-ss", formatSeconds(at),
		"-i", job.Path,
		"-map", "0:" + strconv.Itoa(meta.StreamIndex),
		"-frames:v", "1",
		"-filter_threads", "1", // 简单滤镜线程
		"-vf", scale,
		"-threads", "2", // 编码端线程
		"-q:v", strconv.Itoa(e.Images.JPEGQuality),
		"-update", "1",
		out,
	}
	if err := runFFmpeg(ctx, e.FFmpegPath, args); err != nil {
		if errors.Is(err, errToolVanished) {
			return domain.Frame{}, errToolVanished
		}
		return domain.Frame{}, fmt.Errorf("ffmpeg 失败: %w", err)
	}
	frame, err := verifyJPEG(out, tw, th)
	if err != nil {
		return domain.Frame{}, err
	}
	// 超过单图上限:最多一次重新编码,尺寸缩至 75%,质量 +3
	if int64(frame.Bytes) > e.imageLimit() {
		nw := evenFloor(float64(frame.Width) * 0.75)
		nh := evenFloor(float64(frame.Height) * 0.75)
		q := min(e.Images.JPEGQuality+3, 15)
		re := out + ".re.jpg"
		rerr := runFFmpeg(ctx, e.FFmpegPath, []string{
			"-nostdin", "-hide_banner", "-loglevel", "error",
			"-i", out,
			"-vf", fmt.Sprintf("scale=%d:%d:flags=lanczos", nw, nh),
			"-q:v", strconv.Itoa(q),
			"-update", "1",
			re,
		})
		if rerr != nil {
			return domain.Frame{}, fmt.Errorf("重新编码失败: %w", rerr)
		}
		rframe, verr := verifyJPEG(re, nw, nh)
		if verr != nil {
			return domain.Frame{}, verr
		}
		if int64(rframe.Bytes) > e.imageLimit() {
			return domain.Frame{}, errors.New(string(domain.CodeImageTooLarge) + ": 重新编码后仍超过单图上限")
		}
		rframe.Recompressed = true
		rframe.Path = re
		return rframe, nil
	}
	return frame, nil
}

// runFFmpeg 执行 ffmpeg 并返回错误;WaitDelay 防止孤儿进程悬挂管道。
func runFFmpeg(ctx context.Context, path string, args []string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = 500 * time.Millisecond
	var stderr limitedWriter
	stderr.max = 64 * 1024
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := tailBytes(stderr.buf.Bytes(), domain.DefaultBudgets().StderrTailLimit)
		if errors.Is(err, exec.ErrNotFound) {
			return errToolVanished
		}
		if ctx.Err() != nil {
			return fmt.Errorf("%v (stderr: %s)", ctx.Err(), tail)
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("ffmpeg 退出码 %d (stderr: %s)", exitErr.ExitCode(), tail)
		}
		return fmt.Errorf("%v (stderr: %s)", err, tail)
	}
	return nil
}

// errToolVanished 表示 ffmpeg 在运行中从 PATH 消失,属于全局依赖故障。
var errToolVanished = errors.New("ffmpeg 在运行中消失(PATH 中不再存在)")

// verifyJPEG 重新解码检查 JPEG 有效性、实际宽高与可读性。
func verifyJPEG(path string, wantW, wantH int) (domain.Frame, error) {
	f, err := os.Open(path)
	if err != nil {
		return domain.Frame{}, fmt.Errorf("打开截图失败: %w", err)
	}
	defer f.Close()
	cfg, format, derr := image.DecodeConfig(f)
	if derr != nil || format != "jpeg" {
		return domain.Frame{}, fmt.Errorf("截图不是有效 JPEG: %v", derr)
	}
	if cfg.Width != wantW || cfg.Height != wantH {
		return domain.Frame{}, fmt.Errorf("截图尺寸 %dx%d 与目标 %dx%d 不符", cfg.Width, cfg.Height, wantW, wantH)
	}
	st, serr := os.Stat(path)
	if serr != nil {
		return domain.Frame{}, fmt.Errorf("读取截图大小失败: %w", serr)
	}
	return domain.Frame{Path: path, Width: cfg.Width, Height: cfg.Height, Bytes: int(st.Size())}, nil
}

// formatSeconds 以十进制秒输出 -ss 参数。
func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}
