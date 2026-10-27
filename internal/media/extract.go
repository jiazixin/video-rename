package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder for per-image verification
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

// Extractor extracts planned JPEG frames with FFmpeg.
// Each Extract creates a private temp directory, removed on failure or cancellation and handed to the caller on success.
type Extractor struct {
	TempRoot   string // Optional managed workspace; caller owns the parent.
	FFmpegPath string
	Images     config.ImagesConfig
	Budget     time.Duration // total extraction budget, default 120s
	ImageLimit int64         // per-image byte limit, default 1 MiB
	RunID      string        // component of the temp directory prefix
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

// targetDims computes the output size:
// s = min(1, max_dimension / max(W,H)), never upscaling small images;
// width and height round down to encodable even values of at least 2 pixels; degenerate inputs are rejected.
func targetDims(w, h, maxDim int) (int, int, error) {
	if w < 2 || h < 2 {
		return 0, 0, fmt.Errorf("degenerate video size %dx%d (under 2 pixels)", w, h)
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
	n := int(v) // round down
	if n < 2 {
		return 2
	}
	return n - n%2
}

// Extract extracts frames point by point according to plan.
//
// Behavior (docs/processing.md section 3):
//   - input-side seek per point, explicit map of the absolute stream index, single-frame output;
//   - a failed point is retried once, moved to 1/3 of its segment;
//   - all attempts share the per-video extraction budget; once spent, remaining points are skipped;
//   - an image over the 1 MiB limit is re-encoded at most once (75% size, quality +3);
//   - at least one frame is enough to continue; zero frames fail (extract_timeout when timeouts dominate,
//     otherwise extract_insufficient_frames).
//
// Even on error, set carries completed frames and warnings for logging.
func (e *Extractor) Extract(ctx context.Context, job domain.VideoJob, meta domain.VideoMeta, plan domain.FramePlan) (set domain.FrameSet, jobErr *domain.JobError) {
	set = domain.FrameSet{Plan: plan}
	var diagnostics strings.Builder
	fmt.Fprintf(&diagnostics, "FFmpeg: %s\nvideo: %s\nstream: %d · size: %dx%d · planned frames: %d · budget: %s\n", e.FFmpegPath, job.Path, meta.StreamIndex, meta.DisplayWidth, meta.DisplayHeight, len(plan.Times), e.budget())
	record := func(at time.Duration, attempt string, err error) {
		if err != nil && diagnostics.Len() < 120*1024 {
			fmt.Fprintf(&diagnostics, "\nsample at %s · %s\n%s\n", at, attempt, err)
		}
	}
	defer func() {
		if jobErr != nil {
			fmt.Fprintf(&diagnostics, "\nresult: %s\nframes: %d\nwarnings: %s\n", jobErr.Error(), len(set.Frames), strings.Join(set.Warn, "\n"))
			jobErr.ProcessingLog = diagnostics.String()
		}
	}()
	tw, th, err := targetDims(meta.DisplayWidth, meta.DisplayHeight, e.Images.MaxDimension)
	if err != nil {
		set.Warn = append(set.Warn, err.Error())
		return set, &domain.JobError{Code: domain.CodeExtractFailed, Message: err.Error(), Details: map[string]any{"reason": "degenerate_size", "width": meta.DisplayWidth, "height": meta.DisplayHeight}}
	}

	tmp, merr := os.MkdirTemp(e.TempRoot, "video-rename-"+e.RunID+"-")
	if merr != nil {
		return set, &domain.JobError{Code: domain.CodeExtractFailed, Message: fmt.Sprintf("create temp directory: %v", merr), Details: map[string]any{"reason": "temp_dir", "detail": merr.Error()}}
	}
	// On success the temp directory passes to the caller (frames are still needed for inference);
	// on failure or cancellation it is removed here.
	set.TempDir = tmp
	cleanupNow := true
	defer func() {
		if cleanupNow {
			if rmErr := os.RemoveAll(tmp); rmErr != nil {
				set.Warn = append(set.Warn, fmt.Sprintf("remove temp directory: %v", rmErr))
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
			set.Warn = append(set.Warn, fmt.Sprintf("extraction budget spent; %d sample points not tried", n-i))
			break
		}
		planned := plan.Times[i]
		// retry point: 1/3 of the segment
		retry := plan.WindowStart + time.Duration((float64(i)+1.0/3.0)*float64(seg))
		out := filepath.Join(tmp, fmt.Sprintf("frame-%02d.jpg", i))

		frame, ferr := e.extractPoint(ctx, job, meta, planned, out, tw, th)
		record(planned, "first attempt", ferr)
		if ferr != nil && errors.Is(ferr, errToolVanished) {
			return set, &domain.JobError{
				Code: domain.CodeToolNotFound, Message: errToolVanished.Error(), Details: map[string]any{"tool": "ffmpeg", "reason": "vanished"}, Global: true,
			}
		}
		if ferr != nil && ctx.Err() == nil {
			// first attempt failed: retry once at 1/3 of the segment instead of its midpoint
			out2 := filepath.Join(tmp, fmt.Sprintf("frame-%02d-r.jpg", i))
			frame, ferr = e.extractPoint(ctx, job, meta, retry, out2, tw, th)
			record(retry, "retry", ferr)
			if ferr == nil {
				frame.Planned, frame.Actual = planned, retry
			}
		}
		if errors.Is(ferr, errToolVanished) {
			return set, &domain.JobError{
				Code: domain.CodeToolNotFound, Message: errToolVanished.Error(), Details: map[string]any{"tool": "ffmpeg", "reason": "vanished"}, Global: true,
			}
		}
		if ferr != nil {
			if ctx.Err() == context.Canceled {
				return set, &domain.JobError{Code: domain.CodeCancelled, Message: "extraction cancelled"}
			}
			if ctx.Err() != nil {
				set.Warn = append(set.Warn, fmt.Sprintf("extraction budget spent at point %d", i+1))
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
			return set, &domain.JobError{Code: domain.CodeExtractTimeout, Message: "extraction budget spent without a frame"}
		}
		if n == 0 {
			return set, &domain.JobError{Code: domain.CodeExtractInsufficientFrames, Message: "sample plan is empty", Details: map[string]any{"reason": "empty_plan"}}
		}
		return set, &domain.JobError{Code: domain.CodeExtractInsufficientFrames, Message: fmt.Sprintf("all %d planned frames failed", n), Details: map[string]any{"planned": n}}
	}
	cleanupNow = false // success: the caller removes the temp directory after the video completes
	return set, nil
}

// extractPoint runs one FFmpeg extraction at a single time point and verifies the output.
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
		"-threads", "2", // decoder threads
		"-ss", formatSeconds(at),
		"-i", job.Path,
		"-map", "0:" + strconv.Itoa(meta.StreamIndex),
		"-frames:v", "1",
		"-filter_threads", "1", // simple filter threads
		"-vf", scale,
		"-threads", "2", // encoder threads
		"-q:v", strconv.Itoa(e.Images.JPEGQuality),
		"-update", "1",
		out,
	}
	if err := runFFmpeg(ctx, e.FFmpegPath, args); err != nil {
		if errors.Is(err, errToolVanished) {
			return domain.Frame{}, errToolVanished
		}
		return domain.Frame{}, fmt.Errorf("ffmpeg: %w", err)
	}
	frame, err := verifyJPEG(out, tw, th)
	if err != nil {
		return domain.Frame{}, err
	}
	// over the per-image limit: re-encode at most once at 75% size, quality +3
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
			return domain.Frame{}, fmt.Errorf("re-encode: %w", rerr)
		}
		rframe, verr := verifyJPEG(re, nw, nh)
		if verr != nil {
			return domain.Frame{}, verr
		}
		if int64(rframe.Bytes) > e.imageLimit() {
			return domain.Frame{}, errors.New(string(domain.CodeImageTooLarge) + ": still over the per-image limit after re-encoding")
		}
		rframe.Recompressed = true
		rframe.Path = re
		return rframe, nil
	}
	return frame, nil
}

// runFFmpeg runs ffmpeg and returns its error; WaitDelay keeps orphaned processes from holding pipes.
func runFFmpeg(ctx context.Context, path string, args []string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = 500 * time.Millisecond
	var stderr limitedWriter
	stderr.max = 64 * 1024
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := tailBytes(stderr.buf.Bytes(), domain.DefaultBudgets().StderrTailLimit)
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return errToolVanished
		}
		if ctx.Err() != nil {
			return fmt.Errorf("%w (stderr: %s)", ctx.Err(), tail)
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("ffmpeg exit code %d (stderr: %s)", exitErr.ExitCode(), tail)
		}
		return fmt.Errorf("%w (stderr: %s)", err, tail)
	}
	return nil
}

// errToolVanished reports that ffmpeg disappeared from PATH while running, a global dependency failure.
var errToolVanished = errors.New("ffmpeg disappeared while running (no longer in PATH)")

// verifyJPEG decodes the JPEG again to check validity, actual dimensions and readability.
func verifyJPEG(path string, wantW, wantH int) (domain.Frame, error) {
	f, err := os.Open(path)
	if err != nil {
		return domain.Frame{}, fmt.Errorf("open frame: %w", err)
	}
	defer f.Close()
	cfg, format, derr := image.DecodeConfig(f)
	if derr != nil {
		return domain.Frame{}, fmt.Errorf("decode frame: %w", derr)
	}
	if format != "jpeg" {
		return domain.Frame{}, fmt.Errorf("frame format is %q, want jpeg", format)
	}
	if cfg.Width != wantW || cfg.Height != wantH {
		return domain.Frame{}, fmt.Errorf("frame size %dx%d differs from target %dx%d", cfg.Width, cfg.Height, wantW, wantH)
	}
	st, serr := os.Stat(path)
	if serr != nil {
		return domain.Frame{}, fmt.Errorf("stat frame: %w", serr)
	}
	return domain.Frame{Path: path, Width: cfg.Width, Height: cfg.Height, Bytes: int(st.Size())}, nil
}

// formatSeconds formats the -ss argument as decimal seconds.
func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}
