// Package vision 实现与 llama.cpp llama-server Chat Completions 的 HTTP 交互:
// 多图请求组装、大小预算、有限重试与输出验证。
// 模型模块不修改文件、不重新抽帧;HTTP 重试复用最终帧集和请求体。
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
)

// Client 与 llama-server 通信的客户端。零值不可用,通过 New 构造。
type Client struct {
	settings        config.ServerConfig
	provider        string
	protocol        string
	omitTemperature bool
	endpoint        string
	model           string
	apiKey          string
	temperature     float64
	maxTokens       int
	failureKeywords []string
	timeout         time.Duration

	http        *http.Client
	maxAttempts int // 默认 3
	sleeper     func(ctx context.Context, d time.Duration) error
	jitter      func() float64 // [0,1)

	bodyLimit     int64 // 请求体上限,默认 16 MiB
	responseLimit int64 // 响应体上限,默认 1 MiB
	summaryLimit  int   // 错误响应摘要上限,默认 2 KiB
}

// New 按配置构造客户端。共享连接池;禁用自动重定向;
// 连接超时取 10s 与 server.timeout 的较小值。
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
	return fmt.Errorf("重定向被禁用: %s", req.URL)
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

// Title 对一个视频的帧集请求标题。
// 返回通过单行输出验证的标题;失败返回 *domain.JobError。
// 401/403 返回 Global 标记,用于停止调度新任务。
func (c *Client) Title(ctx context.Context, prompt string, frames []domain.Frame) (result domain.TitleResult, jobErr *domain.JobError) {
	// 所有返回给 pipeline/控制台的错误都在离开客户端前统一脱敏。
	defer func() {
		if jobErr != nil {
			jobErr.Message = safeDiagnostic(jobErr.Message, c.summaryLimit, c.apiKey, prompt)
			jobErr.StderrTail = safeDiagnostic(jobErr.StderrTail, c.summaryLimit, c.apiKey, prompt)
		}
	}()
	if len(frames) == 0 {
		return domain.TitleResult{}, &domain.JobError{Code: domain.CodeRequestTooLarge, Message: "零帧不调用模型"}
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
				verr.RawRequest = diagnosticRequest(body, c.apiKey)
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
		// 退避:基数 min(15s, 2s × 2^(k−1)) 加 0.5–1 倍 jitter;
		// Retry-After 是最小等待要求,与本地退避取较大值。
		base := min(15*time.Second, (1<<uint(attempt-1))*2*time.Second)
		wait := max(rerr.RetryAfter, time.Duration(float64(base)*(0.5+0.5*c.jitter())))
		if wait > 15*time.Second {
			lastErr.Message += fmt.Sprintf("; Retry-After %v 超过 15s 上限,停止重试", rerr.RetryAfter)
			return domain.TitleResult{Attempts: attempt, FramesSent: sent}, &lastErr.JobError
		}
		if err := c.sleeper(ctx, wait); err != nil {
			return domain.TitleResult{Attempts: attempt, FramesSent: sent}, &domain.JobError{
				Code: domain.CodeCancelled, Message: "等待重试时被取消",
			}
		}
	}
	return domain.TitleResult{Attempts: c.maxAttempts, FramesSent: sent}, &lastErr.JobError
}

// buildBody 组装请求体;超预算时均匀删帧(保持时间顺序),
// 低于最低有效帧数(1)则 REQUEST_TOO_LARGE。
// 组装后按真正序列化字节数复核 16 MiB 上限;返回实际保留的帧数。
func (c *Client) buildBody(prompt string, frames []domain.Frame) ([]byte, int, *domain.JobError) {
	for {
		parts, readErr := c.contentParts(frames)
		if readErr != nil {
			return nil, 0, readErr
		}
		request := any(chatRequest{
			Model: c.model,
			Messages: []chatMessage{
				// system 只发送业务 prompt（所选提示词 + 固定提示词）；
				// 命名失败协议含在固定提示词默认值中，页面可编辑。
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
					fields[k], _ = json.Marshal(v)
				}
				body, err = json.Marshal(fields)
			}
		}
		if err != nil {
			return nil, 0, &domain.JobError{Code: domain.CodeAPIResponseInvalid, Message: fmt.Sprintf("组装请求失败: %v", err)}
		}
		if int64(len(body)) <= c.bodyLimit {
			return body, len(frames), nil
		}
		if len(frames) <= 1 {
			return nil, 0, &domain.JobError{
				Code:    domain.CodeRequestTooLarge,
				Message: fmt.Sprintf("单帧请求体 %d 字节仍超过 %d 上限", len(body), c.bodyLimit),
			}
		}
		// 均匀删除:保留偶数下标帧
		kept := make([]domain.Frame, 0, (len(frames)+1)/2)
		for i := 0; i < len(frames); i += 2 {
			kept = append(kept, frames[i])
		}
		frames = kept
	}
}

// contentParts 构造当前视频的多模态内容，不重复或改写 system 中的命名要求。
// 其后按时间顺序交替放置时间说明与 image_url(data URL)。
func (c *Client) contentParts(frames []domain.Frame) ([]contentPart, *domain.JobError) {
	parts := make([]contentPart, 0, 1+2*len(frames))
	parts = append(parts, contentPart{Type: "text", Text: "以下截图来自同一个视频，按时间顺序排列。"})
	for i, fr := range frames {
		image, err := dataURL(fr.Path)
		if err != nil {
			return nil, &domain.JobError{Code: domain.CodeExtractFailed, Message: fmt.Sprintf("第 %d 张截图无法读取或为空，请重新抽帧", i+1)}
		}
		ts := formatTimestamp(fr.Actual)
		parts = append(parts, contentPart{Type: "text", Text: fmt.Sprintf("第 %d/%d 张截图,时间点 %s。", i+1, len(frames), ts)})
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: image}})
	}
	return parts, nil
}

// dataURL 读取 JPEG 文件并编码为 data URL。
func dataURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("截图为空")
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

// ---- JSON 协议结构 ----

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
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			// ToolCalls 用类型化切片而非 jsontext.Value:网关常返回
			// "tool_calls": null,RawMessage 会存下字面量 "null"(len=4),
			// 导致误判为包含工具调用;切片对 null/[] 都解码为长度 0。
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

// doOnce 执行单次 HTTP 请求,返回原始标题文本。
func (c *Client) doOnce(ctx context.Context, body []byte, capture ...*domain.ModelDiagnostics) (title string, status int, jobErr *httpJobError) {
	var raw []byte
	defer func() {
		if jobErr == nil && len(capture) > 0 {
			capture[0].Request = diagnosticRequest(body, c.apiKey)
			capture[0].Response = diagnosticBody(raw, c.apiKey)
		}
		if jobErr != nil {
			jobErr.RawRequest = diagnosticRequest(body, c.apiKey)
			jobErr.RawResponse = diagnosticBody(raw, c.apiKey)
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, &httpJobError{Code: domain.CodeAPIHTTPError, Message: fmt.Sprintf("构造请求失败: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	if c.protocol == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if c.apiKey != "" {
			req.Header.Set("x-api-key", c.apiKey)
		}
	} else if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, transportError(ctx, err, 0)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, c.responseLimit+1)
	var rerr error
	raw, rerr = io.ReadAll(limited)
	if rerr != nil {
		if ctx.Err() != nil || (resp.StatusCode >= 200 && resp.StatusCode <= 299) {
			return "", resp.StatusCode, transportError(ctx, rerr, resp.StatusCode)
		}
		// 已知的鉴权/请求错误不因响应体中断而变成可重试请求。
		return c.errorForStatus(resp, nil)
	}
	if int64(len(raw)) > c.responseLimit {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return c.errorForStatus(resp, nil)
		}
		return "", resp.StatusCode, &httpJobError{Code: domain.CodeAPIResponseTooLarge, Message: fmt.Sprintf("响应体超过 %d 字节上限", c.responseLimit)}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
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
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", resp.StatusCode, &httpJobError{Code: domain.CodeAPIResponseInvalid, Message: "响应 JSON 格式或字段类型无效"}
	}
	if len(cr.Choices) == 0 {
		msg := "choices 为空"
		if cr.Error != nil && cr.Error.Message != "" {
			msg += "; 服务端错误: " + cr.Error.Message
		}
		return "", resp.StatusCode, &httpJobError{Code: domain.CodeAPIResponseInvalid, Message: msg}
	}
	choice := cr.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		return "", resp.StatusCode, &httpJobError{Code: domain.CodeAPIResponseInvalid, Message: "响应包含 tool_calls,不是文本标题"}
	}
	if choice.FinishReason == "length" {
		return "", resp.StatusCode, &httpJobError{Code: domain.CodeModelOutputTruncated, Message: "模型输出因长度截断,不作为标题"}
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return "", resp.StatusCode, &httpJobError{Code: domain.CodeAPIResponseInvalid, Message: "message.content 为空"}
	}
	if _, validationErr := ValidateTitle(choice.Message.Content, c.failureKeywords); validationErr != nil {
		return "", resp.StatusCode, &httpJobError{JobError: *validationErr}
	}
	return choice.Message.Content, resp.StatusCode, nil
}

// errorForStatus 将非 2xx 响应映射为可重试/不可重试错误。
func (c *Client) errorForStatus(resp *http.Response, raw []byte) (string, int, *httpJobError) {
	summary := responseDiagnostic(raw)
	e := &httpJobError{
		Code:       domain.CodeAPIHTTPError,
		HTTPStatus: resp.StatusCode,
		StderrTail: summary,
	}
	switch resp.StatusCode {
	case 408, 429, 500, 502, 503, 504:
		e.Code = domain.CodeAPIRateLimited // 429;其余在最终失败时改写
		if resp.StatusCode != 429 {
			e.Code = domain.CodeAPIHTTPError
		}
		e.Message = fmt.Sprintf("HTTP %d(可重试)", resp.StatusCode)
		e.RetryableFlag = true
		if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
			e.RetryAfter = ra
		}
	case 401, 403:
		e.Code = domain.CodeAPIUnauthorized
		e.Message = "鉴权失败(不重试,停止新任务)"
		e.Global = true
	case 400, 404, 413, 422:
		e.Message = fmt.Sprintf("HTTP %d(不重试;请核对 base_url/model/请求规模配置)", resp.StatusCode)
	default:
		e.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return "", resp.StatusCode, e
}

// parseRetryAfter 解析秒数或 HTTP 日期格式的 Retry-After。
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

// transportError 对发送和读取阶段采用相同的取消/超时/重试语义。
// 错误文本可能包含请求头或重定向 URL，诊断仅输出固定分类。
func transportError(ctx context.Context, err error, status int) *httpJobError {
	if ctx.Err() != nil {
		return &httpJobError{Code: domain.CodeCancelled, HTTPStatus: status, Message: "请求被取消"}
	}
	code, message := domain.CodeAPIHTTPError, "HTTP 传输失败"
	if isTimeoutErr(err) {
		code, message = domain.CodeAPITimeout, "HTTP 请求或响应体读取超时"
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
