package domain

import "context"

// ModelDiagnostics carries a single video's sanitized model exchange to its committer.
type ModelDiagnostics struct {
	Request  string
	Response string
}
type diagnosticsKey struct{}

func WithModelDiagnostics(ctx context.Context, d *ModelDiagnostics) context.Context {
	return context.WithValue(ctx, diagnosticsKey{}, d)
}
func CaptureModelDiagnostics(ctx context.Context, result TitleResult) {
	if d, ok := ctx.Value(diagnosticsKey{}).(*ModelDiagnostics); ok {
		d.Request = result.RawRequest
		d.Response = result.RawResponse
	}
}
