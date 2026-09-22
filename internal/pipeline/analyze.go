package pipeline

import (
	"context"
	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/media"
	"video-rename/internal/vision"
)

// Analyze reuses the existing bounded probe/extract/inference pipeline without committing files.
func Analyze(ctx context.Context, cfg *config.Config, job domain.VideoJob, probe, ffmpeg string) (string, error) {
	rec := processJob(ctx, job, Options{Config: cfg}, &media.Prober{FFprobePath: probe}, media.NewSampler(cfg.Sampling), &media.Extractor{FFmpegPath: ffmpeg, Images: cfg.Images, RunID: "web"}, vision.New(cfg))
	if rec.jobErr != nil {
		return "", rec.jobErr
	}
	domain.CaptureModelDiagnostics(ctx, rec.title)
	return rec.title.Title, nil
}
