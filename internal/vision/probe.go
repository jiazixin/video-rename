package vision

import (
	"context"
	"video-rename/internal/domain"
)

// Probe performs exactly one inference, using the same protocol and parameters as a run.
func (c *Client) Probe(ctx context.Context, prompt string, frame domain.Frame) (string, int, *domain.JobError) {
	body, _, err := c.buildBody(prompt, []domain.Frame{frame})
	if err != nil {
		return "", 0, err
	}
	text, status, httpErr := c.doOnce(ctx, body)
	if httpErr != nil {
		httpErr.Message = safeDiagnostic(httpErr.Message, c.summaryLimit, c.apiKey, prompt)
		return "", status, &httpErr.JobError
	}
	return safeDiagnostic(text, 4096, c.apiKey), status, nil
}
