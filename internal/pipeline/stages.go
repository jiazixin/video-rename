package pipeline

import (
	"context"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/media"
	"video-rename/internal/vision"
)

// PrepareRequest supplies the source, extraction configuration and owned workspace.
type PrepareRequest struct {
	Config     *config.TaskConfig
	Job        domain.VideoJob
	ProbePath  string
	FFmpegPath string
	WorkDir    string
}

// Prepare is independent of model concurrency. Successful output belongs to the cache.
func Prepare(ctx context.Context, request PrepareRequest) (domain.FrameSet, error) {
	cfg, job, probe, ffmpeg, work := request.Config, request.Job, request.ProbePath, request.FFmpegPath, request.WorkDir
	meta, err := (&media.Prober{FFprobePath: probe}).Probe(ctx, job)
	if err != nil {
		return domain.FrameSet{}, err
	}
	plan, e := media.NewSampler(cfg.Sampling).Plan(meta.Duration, meta.SizeBytes)
	if e != nil {
		return domain.FrameSet{}, e
	}
	set, err := (&media.Extractor{FFmpegPath: ffmpeg, Images: cfg.Images, TempRoot: work, RunID: "web"}).Extract(ctx, job, meta, plan)
	if err != nil {
		return set, err
	}
	return set, nil
}

// Infer asks the configured model for a title within the per-video budget.
func Infer(ctx context.Context, cfg *config.TaskConfig, frames []domain.Frame) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, domain.JobBudget(cfg.TimeoutDuration))
	defer cancel()
	client := vision.New(cfg)
	defer client.Close()
	result, err := client.TitleOnce(ctx, cfg.Prompt, frames)
	if err != nil {
		return "", err
	}
	domain.CaptureModelDiagnostics(ctx, result)
	return result.Title, nil
}
