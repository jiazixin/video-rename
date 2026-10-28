// Package vision talks HTTP to the llama.cpp llama-server Chat Completions API:
// multi-image request assembly, size budgets, bounded retries and output validation.
// It never modifies files or re-extracts frames; HTTP retries reuse the final frame set and request body.
package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/secret"
)

// Client talks to llama-server. The zero value is unusable; construct it with New.
type Client struct {
	settings        config.ServerConfig
	provider        string
	protocol        string
	omitTemperature bool
	endpoint        string
	model           string
	apiKey          secret.Value
	temperature     float64
	maxTokens       int
	failureKeywords []string
	timeout         time.Duration

	http        *http.Client
	maxAttempts int // default 3
	sleeper     func(ctx context.Context, d time.Duration) error
	jitter      func() float64 // [0,1)

	bodyLimit     int64 // request body limit, default 16 MiB
	responseLimit int64 // response body limit, default 1 MiB
	summaryLimit  int   // error response summary limit, default 2 KiB
}

// New builds a client from cfg. It shares a connection pool and disables automatic redirects;
// the connect timeout is the smaller of 10s and server.timeout.
func New(cfg *config.TaskConfig) *Client {
	timeout := cfg.TimeoutDuration
	dialTimeout := min(timeout, 10*time.Second)
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: dialTimeout}).DialContext,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}
	c := &Client{
		settings:        cfg.Server,
		provider:        cfg.Server.Provider,
		protocol:        cfg.Server.Protocol,
		omitTemperature: cfg.Server.OmitTemperature,
		endpoint:        cfg.Endpoint,
		model:           cfg.Server.Model,
		apiKey:          cfg.Server.APIKey,
		temperature:     cfg.Server.Temperature,
		maxTokens:       cfg.Server.MaxTokens,
		failureKeywords: slices.Clone(cfg.FailureKeywords),
		timeout:         timeout,
		http:            &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: rejectRedirect},
		maxAttempts:     3,
		sleeper:         sleepContext,
		jitter:          rand.Float64,
		bodyLimit:       domain.DefaultBudgets().RequestBodyLimit,
		responseLimit:   domain.DefaultBudgets().ResponseBodyLim,
		summaryLimit:    domain.DefaultBudgets().ResponseSummary,
	}
	return c
}

// Close releases connections retained after this client's inference finishes.
func (c *Client) Close() { c.http.CloseIdleConnections() }

func rejectRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// TitleOnce performs one request; jobs owns the retry budget and fallback selection.
func (c *Client) TitleOnce(ctx context.Context, prompt string, frames []domain.Frame) (domain.TitleResult, *domain.JobError) {
	single := *c
	single.maxAttempts = 1
	return single.Title(ctx, prompt, frames)
}

// Title requests a title for the frame set of one video.
// It returns a title that passed single-line output validation; failures are *domain.JobError.
// 401/403 set Global so that no new jobs are scheduled.
func (c *Client) Title(ctx context.Context, prompt string, frames []domain.Frame) (result domain.TitleResult, jobErr *domain.JobError) {
	// every error returned to the pipeline or console is redacted before it leaves the client.
	defer func() {
		if jobErr != nil {
			jobErr.Message = safeDiagnostic(jobErr.Message, c.summaryLimit, c.apiKey.Reveal(), prompt)
			jobErr.StderrTail = safeDiagnostic(jobErr.StderrTail, c.summaryLimit, c.apiKey.Reveal(), prompt)
		}
	}()
	if len(frames) == 0 {
		return domain.TitleResult{}, &domain.JobError{Code: domain.CodeRequestTooLarge, Message: "no frames; model not called", Details: map[string]any{"reason": "no_frames"}}
	}
	body, sent, jerr := c.buildBody(prompt, frames)
	if jerr != nil {
		return domain.TitleResult{}, jerr
	}

	var lastErr *httpJobError
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		var exchange domain.ModelDiagnostics
		content, _, rerr := c.doOnce(ctx, body, &exchange)
		if rerr == nil {
			title, verr := ValidateTitle(content, c.failureKeywords)
			if verr != nil {
				verr.HTTPStatus = 0
				verr.RawRequest = diagnosticRequest(body, c.apiKey.Reveal())
				verr.RawResponse = exchange.Response
				return domain.TitleResult{Attempts: attempt, FramesSent: sent}, verr
			}
			return domain.TitleResult{Title: title, Attempts: attempt, FramesSent: sent, RawRequest: exchange.Request, RawResponse: exchange.Response}, nil
		}
		rerr.JobError.RetryAfter = rerr.RetryAfter
		lastErr = rerr
		if !rerr.Retryable() {
			return domain.TitleResult{Attempts: attempt, FramesSent: sent}, &rerr.JobError
		}
		if attempt == c.maxAttempts {
			break
		}
		// backoff: base min(15s, 2s × 2^(k−1)) times a 0.5–1 jitter;
		// Retry-After is a minimum wait, so the larger of it and the local backoff applies.
		base := min(15*time.Second, (1<<uint(attempt-1))*2*time.Second)
		wait := max(rerr.RetryAfter, time.Duration(float64(base)*(0.5+0.5*c.jitter())))
		if wait > 15*time.Second {
			lastErr.Message += fmt.Sprintf("; Retry-After %v exceeds 15s; retries stopped", rerr.RetryAfter)
			return domain.TitleResult{Attempts: attempt, FramesSent: sent}, &lastErr.JobError
		}
		if err := c.sleeper(ctx, wait); err != nil {
			return domain.TitleResult{Attempts: attempt, FramesSent: sent}, &domain.JobError{
				Code: domain.CodeCancelled, Message: "cancelled while waiting to retry",
			}
		}
	}
	return domain.TitleResult{Attempts: c.maxAttempts, FramesSent: sent}, &lastErr.JobError
}

// buildBody assembles the request body; over budget it drops frames evenly (keeping time order),
// and below the minimum of one usable frame it reports request_too_large.
// The 16 MiB limit is checked again on the serialized bytes; it returns the number of frames kept.
func (c *Client) buildBody(prompt string, frames []domain.Frame) ([]byte, int, *domain.JobError) {
	for {
		parts, readErr := c.contentParts(frames)
		if readErr != nil {
			return nil, 0, readErr
		}
		request := any(chatRequest{
			Model: c.model,
			Messages: []chatMessage{
				// system carries only the business prompt (selected prompt + fixed prompt);
				// the naming failure protocol is part of the editable fixed prompt default.
				{Role: "system", Content: strings.TrimRight(prompt, "\n")},
				{Role: "user", Content: parts},
			},
			Temperature: c.temperature,
			MaxTokens:   c.maxTokens,
			Stream:      false,
		})
		if c.protocol == "anthropic" {
			request = c.anthropicRequest(prompt, parts)
		}
		body, err := json.Marshal(request)
		if err == nil {
			var fields map[string]jsontext.Value
			err = json.Unmarshal(body, &fields)
			if err == nil {
				if c.omitTemperature || c.settings.Thinking == "enabled" {
					delete(fields, "temperature")
				}
				if c.provider == "openai" {
					fields["max_completion_tokens"] = fields["max_tokens"]
					delete(fields, "max_tokens")
				}
				controls, controlErr := c.settings.ThinkingFields()
				if controlErr != nil {
					return nil, 0, &domain.JobError{Code: domain.CodeAPIResponseInvalid, Message: controlErr.Error()}
				}
				for k, v := range controls {
					encoded, marshalErr := json.Marshal(v)
					if marshalErr != nil {
						return nil, 0, &domain.JobError{Code: domain.CodeAPIResponseInvalid, Message: marshalErr.Error()}
					}
					fields[k] = encoded
				}
				body, err = json.Marshal(fields)
			}
		}
		if err != nil {
			return nil, 0, &domain.JobError{Code: domain.CodeAPIResponseInvalid, Message: fmt.Sprintf("build request: %v", err), Details: map[string]any{"reason": "request_build", "detail": err.Error()}}
		}
		if int64(len(body)) <= c.bodyLimit {
			return body, len(frames), nil
		}
		if len(frames) <= 1 {
			return nil, 0, &domain.JobError{
				Code:    domain.CodeRequestTooLarge,
				Message: fmt.Sprintf("single-frame request body of %d bytes exceeds the %d limit", len(body), c.bodyLimit),
				Details: map[string]any{"bytes": len(body), "limit": c.bodyLimit},
			}
		}
		// drop evenly: keep frames at even indexes
		kept := make([]domain.Frame, 0, (len(frames)+1)/2)
		for i := 0; i < len(frames); i += 2 {
			kept = append(kept, frames[i])
		}
		frames = kept
	}
}

// contentParts builds the multimodal content of the current video without repeating or rewriting the naming rules in system.
// It alternates time captions and image_url (data URL) entries in time order.
// The Chinese captions are prompt text for the model, a recorded exception to
// the error-code rule (README, code exceptions).
func (c *Client) contentParts(frames []domain.Frame) ([]contentPart, *domain.JobError) {
	parts := make([]contentPart, 0, 1+2*len(frames))
	parts = append(parts, contentPart{Type: "text", Text: "以下截图来自同一个视频，按时间顺序排列。"})
	for i, fr := range frames {
		image, err := dataURL(fr.Path)
		if err != nil {
			return nil, &domain.JobError{Code: domain.CodeExtractFailed, Message: fmt.Sprintf("frame %d is unreadable or empty; extract again", i+1), Details: map[string]any{"reason": "frame_unreadable", "frame": i + 1}}
		}
		ts := formatTimestamp(fr.Actual)
		parts = append(parts, contentPart{Type: "text", Text: fmt.Sprintf("第 %d/%d 张截图,时间点 %s。", i+1, len(frames), ts)})
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: image}})
	}
	return parts, nil
}

// dataURL reads a JPEG file and encodes it as a data URL.
func dataURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("frame is empty")
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data), nil
}

func formatTimestamp(d time.Duration) string {
	total := d.Milliseconds()
	h := total / 3600000
	m := (total % 3600000) / 60000
	s := float64(total%60000) / 1000.0
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%06.3f", h, m, s)
	}
	return fmt.Sprintf("%02d:%06.3f", m, s)
}

// ---- JSON protocol types ----

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitzero"`
	ImageURL *imageURL `json:"image_url,omitzero"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			// ToolCalls is a typed slice rather than jsontext.Value: gateways often return
			// "tool_calls": null, and a raw value would store the literal "null" (len=4),
			// looking like a tool call; a slice decodes both null and [] to length 0.
			Content   string           `json:"content"`
			ToolCalls []jsontext.Value `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// doOnce performs a single HTTP request and returns the raw title text.
func (c *Client) doOnce(ctx context.Context, body []byte, capture ...*domain.ModelDiagnostics) (title string, status int, jobErr *httpJobError) {
	var raw []byte
	defer func() {
		if jobErr == nil && len(capture) > 0 {
			capture[0].Request = diagnosticRequest(body, c.apiKey.Reveal())
			capture[0].Response = diagnosticBody(raw, c.apiKey.Reveal())
		}
		if jobErr != nil {
			jobErr.RawRequest = diagnosticRequest(body, c.apiKey.Reveal())
			jobErr.RawResponse = diagnosticBody(raw, c.apiKey.Reveal())
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, &httpJobError{Code: domain.CodeAPIHTTPError, Message: fmt.Sprintf("build request: %v", err), Details: map[string]any{"reason": "request_build", "detail": err.Error()}}
	}
	req.Header.Set("Content-Type", "application/json")
	if c.protocol == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if c.apiKey != "" {
			req.Header.Set("x-api-key", c.apiKey.Reveal())
		}
	} else if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey.Reveal())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, transportError(ctx, err, 0)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, c.responseLimit+1)
	var rerr error
	raw, rerr = io.ReadAll(limited)
	// EOF can win the transport race with cancellation. Do not parse or accept
	// a response after its caller has already abandoned the request.
	if err := ctx.Err(); err != nil {
		return "", resp.StatusCode, transportError(ctx, err, resp.StatusCode)
	}
	if rerr != nil {
		if ctx.Err() != nil || (resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices) {
			return "", resp.StatusCode, transportError(ctx, rerr, resp.StatusCode)
		}
		// known authentication or request errors stay non-retryable even if the response body is cut off.
		return c.errorForStatus(resp, nil)
	}
	if int64(len(raw)) > c.responseLimit {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return c.errorForStatus(resp, nil)
		}
		return "", resp.StatusCode, &httpJobError{Code: domain.CodeAPIResponseTooLarge, Message: fmt.Sprintf("response body exceeds %d bytes", c.responseLimit), Details: map[string]any{"limit": c.responseLimit}}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.errorForStatus(resp, raw)
	}

	if c.protocol == "anthropic" {
		text, code, parseErr := parseAnthropic(raw, resp.StatusCode)
		if parseErr != nil {
			return text, code, parseErr
		}
		if _, validationErr := ValidateTitle(text, c.failureKeywords); validationErr != nil {
			return "", code, &httpJobError{JobError: *validationErr}
		}
		return text, code, nil
	}
	return parseOpenAI(raw, resp.StatusCode, c.failureKeywords)
}

// errorForStatus maps a non-2xx response to a retryable or non-retryable error.
func (c *Client) errorForStatus(resp *http.Response, raw []byte) (string, int, *httpJobError) {
	summary := responseDiagnostic(raw)
	e := &httpJobError{
		Code:       domain.CodeAPIHTTPError,
		HTTPStatus: resp.StatusCode,
		StderrTail: summary,
	}
	switch resp.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		e.Code = domain.CodeAPIRateLimited // 429; others are rewritten on final failure
		if resp.StatusCode != http.StatusTooManyRequests {
			e.Code = domain.CodeAPIHTTPError
		}
		e.Message = fmt.Sprintf("HTTP %d (retryable)", resp.StatusCode)
		e.RetryableFlag = true
		if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
			e.RetryAfter = ra
		}
	case 401, 403:
		e.Code = domain.CodeAPIUnauthorized
		e.Message = "authentication failed (not retried; new jobs stop)"
		e.Global = true
	case 400, 404, 413, 422:
		e.Message = fmt.Sprintf("HTTP %d (not retried; check base_url, model and request size)", resp.StatusCode)
		e.Details = map[string]any{"reason": "check_config"}
	default:
		e.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return "", resp.StatusCode, e
}

// parseRetryAfter parses a Retry-After value in seconds or HTTP date form.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseUint(v, 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		return time.Duration(min(n, uint64((1<<63-1)/time.Second))) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// transportError applies the same cancellation, timeout and retry semantics to sending and reading.
// Error text may contain request headers or redirect URLs, so diagnostics only report a fixed category.
func transportError(ctx context.Context, err error, status int) *httpJobError {
	if ctx.Err() != nil {
		return &httpJobError{Code: domain.CodeCancelled, HTTPStatus: status, Message: "request cancelled"}
	}
	code, message := domain.CodeAPIHTTPError, "HTTP transport failed"
	if isTimeoutErr(err) {
		code, message = domain.CodeAPITimeout, "HTTP request or response body read timed out"
	}
	return &httpJobError{Code: code, HTTPStatus: status, Message: message, RetryableFlag: true}
}

func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	netErr, ok := errors.AsType[net.Error](err)
	return ok && netErr.Timeout()
}
