package pipeline

import (
	"context"
	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/media"
	"video-rename/internal/vision"
)

// Prepare is independent of model concurrency. Successful output belongs to the cache.
func Prepare(ctx context.Context, cfg *config.Config, job domain.VideoJob, probe, ffmpeg, work string) (domain.FrameSet, error) {
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
func Infer(ctx context.Context, cfg *config.Config, frames []domain.Frame) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, domain.JobBudget(cfg.TimeoutDuration))
	defer cancel()
	result, err := vision.New(cfg).TitleOnce(ctx, cfg.Prompt, frames)
	if err != nil {
		return "", err
	}
	domain.CaptureModelDiagnostics(ctx, result)
	return result.Title, nil
}
