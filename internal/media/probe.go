// Package media wraps ffprobe probing, frame sampling plans and FFmpeg JPEG extraction.
// It never makes HTTP calls; external commands start from argument arrays, never shell strings.
package media

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"video-rename/internal/domain"
)

// ffprobeOutput is the relevant subset of ffprobe JSON output.
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

// Prober runs ffprobe. All limits can be injected for tests; zero values use built-in defaults.
type Prober struct {
	FFprobePath string
	Timeout     time.Duration // probe timeout, default 30s
	StdoutLimit int64         // stdout limit, default 4 MiB
	StderrTail  int           // stderr kept, default 8 KiB
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

// limitedWriter accepts at most max bytes, then stops recording and marks overflow;
// the writer (a process pipe) then gets an error so the child process is terminated.
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
		return len(b), nil // drop the rest; the next write fails
	}
	w.buf.Write(b)
	return len(b), nil
}

// Probe probes a single video.
// Errors are *domain.JobError values distinguishing execution failure, timeout, output overflow, invalid JSON,
// no video stream and unknown duration; a failed size check before commit reports source_changed.
func (p *Prober) Probe(ctx context.Context, job domain.VideoJob) (domain.VideoMeta, *domain.JobError) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, p.FFprobePath,
		"-v", "error", "-show_format", "-show_streams", "-of", "json", job.Path)
	// if an orphaned child still holds the pipes after cancellation, WaitDelay reclaims them instead of hanging
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
			Message: fmt.Sprintf("ffprobe stdout exceeds %d bytes", p.stdoutLimit()),
			Details: map[string]any{"limit": p.stdoutLimit()},
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return domain.VideoMeta{}, &domain.JobError{
			Code:       domain.CodeProbeTimeout,
			Message:    "ffprobe timed out",
			StderrTail: string(tail),
		}
	}
	if ctx.Err() == context.Canceled {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeCancelled,
			Message: "probe cancelled",
		}
	}
	if runErr != nil {
		exitCode := 0
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			exitCode = exitErr.ExitCode()
		}
		je := &domain.JobError{
			Code:       domain.CodeProbeExecFailed,
			Message:    fmt.Sprintf("ffprobe failed: %v", runErr),
			ExitCode:   exitCode,
			StderrTail: string(tail),
		}
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) {
			je.Code = domain.CodeToolNotFound
			je.Message = "ffprobe disappeared while running (no longer in PATH)"
			je.Details = map[string]any{"tool": "ffprobe", "reason": "vanished"}
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

// parseProbeOutput parses ffprobe JSON, selects the stream and derives values.
func parseProbeOutput(data []byte, job domain.VideoJob) (domain.VideoMeta, *domain.JobError) {
	var out ffprobeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeJSONInvalid,
			Message: fmt.Sprintf("parse ffprobe JSON: %v", err),
			Details: map[string]any{"reason": "json", "detail": err.Error()},
		}
	}
	// stream selection: the first default, non-cover video stream; otherwise the first non-cover video stream.
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
			Message: "no usable video stream (audio only, cover art only or no video track)",
		}
	}

	// duration: prefer the selected stream's duration, falling back to format.duration.
	duration, ok := parseFiniteSeconds(chosen.Duration)
	if !ok {
		duration, ok = parseFiniteSeconds(out.Format.Duration)
	}
	if !ok || duration <= 0 {
		return domain.VideoMeta{}, &domain.JobError{
			Code:    domain.CodeProbeDurationUnknown,
			Message: "no finite positive duration (stream and format both missing or invalid)",
		}
	}

	meta := domain.VideoMeta{
		StreamIndex: chosen.Index,
		Duration:    time.Duration(duration * float64(time.Second)),
		SizeBytes:   job.Snapshot.Size,
		Width:       chosen.Width,
		Height:      chosen.Height,
	}

	// SAR: missing, "N/A" and "0:1" mean square pixels; anything else must be interpretable.
	sarNum, sarDen := 1, 1
	sar := strings.TrimSpace(chosen.SampleAspectRatio)
	if sar != "" && sar != "N/A" && sar != "0:1" && sar != "0" {
		n, d, err := parseRatio(sar)
		if err != nil || n < 0 || d <= 0 {
			return domain.VideoMeta{}, &domain.JobError{
				Code:    domain.CodeProbeJSONInvalid,
				Message: fmt.Sprintf("cannot interpret sample_aspect_ratio %q", chosen.SampleAspectRatio),
				Details: map[string]any{"reason": "sample_aspect_ratio", "value": chosen.SampleAspectRatio},
			}
		}
		sarNum, sarDen = n, d
	}
	meta.SARNum, meta.SARDen = sarNum, sarDen

	// rotation: prefer the side_data_list display matrix, falling back to tags.rotate.
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
			Message: fmt.Sprintf("cannot interpret rotation of %v degrees", rotDeg),
			Details: map[string]any{"reason": "rotation", "value": rotDeg},
		}
	}
	meta.Rotation = rot

	// display size = coded size × SAR, swapping width and height for 90/270 degree rotation.
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
			Message: "video stream coded size is invalid",
			Details: map[string]any{"reason": "coded_size"},
		}
	}

	// HDR transfer characteristic detection (warning only, no tone mapping).
	switch chosen.ColorTransfer {
	case "smpte2084", "arib-std-b67":
		meta.HDR = true
		meta.HDRNotToneMap = true
	}

	// size check: when clearly different from format.size, check again whether the file changed.
	if fs, err := strconv.ParseInt(strings.TrimSpace(out.Format.Size), 10, 64); err == nil && fs >= 0 && fs != job.Snapshot.Size {
		cur, statErr := domain.StatSnapshot(job.Path)
		switch {
		case statErr != nil:
			return domain.VideoMeta{}, &domain.JobError{
				Code:    domain.CodeSourceChanged,
				Message: fmt.Sprintf("recheck file: %v", statErr),
				Details: map[string]any{"reason": "recheck_failed", "detail": statErr.Error()},
			}
		case !cur.Same(job.Snapshot):
			return domain.VideoMeta{}, &domain.JobError{
				Code:    domain.CodeSourceChanged,
				Message: "file changed during probe",
				Details: map[string]any{"reason": "modified_during_probe"},
			}
		}
		// identity unchanged but ffprobe reports another size: trust Go stat and continue.
	}
	return meta, nil
}

// parseFiniteSeconds parses an ffprobe seconds string, which must be finite and parseable.
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

// parseRatio parses "num:den".
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

// tailBytes returns at most the last n bytes.
func tailBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}
