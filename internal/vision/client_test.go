package vision

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

func testConfig(t *testing.T, baseURL string) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Server: config.ServerConfig{
			BaseURL:     baseURL,
			Model:       "test-model",
			Temperature: 0.2,
			MaxTokens:   128,
		},
		Prompt:          "测试提示词",
		Workers:         2,
		TimeoutDuration: 2 * time.Second,
	}
	cfg.Endpoint = strings.TrimRight(baseURL, "/") + "/chat/completions"
	return cfg
}

func frameSet(n int) []domain.Frame {
	dir, _ := os.MkdirTemp("", "vision-test-")
	frames := make([]domain.Frame, n)
	for i := range frames {
		p := filepath.Join(dir, fmt.Sprintf("f%d.jpg", i))
		os.WriteFile(p, make([]byte, 400), 0o644)
		frames[i] = domain.Frame{Path: p, Width: 64, Height: 64, Bytes: 400, Actual: time.Duration(i) * time.Second}
	}
	return frames
}

// okResponse 构造标准成功响应。
func okResponse(title string) string {
	content, _ := json.Marshal(title)
	return fmt.Sprintf(`{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, content)
}

func newTestClient(cfg *config.Config) *Client {
	c := New(cfg)
	// 注入即时睡眠,测试不等待真实退避
	c.sleeper = func(ctx context.Context, d time.Duration) error {
		return sleepContext(ctx, 0)
	}
	c.jitter = func() float64 { return 0.5 }
	return c
}

func TestTitleHappyPath(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.UnmarshalRead(r.Body, &gotBody)
		w.Write([]byte(okResponse("  海边散步  ")))
	}))
	defer srv.Close()

	cfg := testConfig(t, srv.URL)
	cfg.Server.APIKey = "secret-key"
	c := newTestClient(cfg)
	res, jerr := c.Title(t.Context(), cfg.Prompt, frameSet(3))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if res.Title != "海边散步" {
		t.Errorf("标题 = %q", res.Title)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("请求路径 = %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("鉴权头 = %q", gotAuth)
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("expected system + user, got %d", len(msgs))
	}
	system := msgs[0].(map[string]any)
	if system["role"] != "system" || system["content"] != cfg.Prompt {
		t.Fatalf("wrong system prompt: %v", system)
	}
	user := msgs[1].(map[string]any)
	if user["role"] != "user" {
		t.Fatal(user)
	}
	parts := user["content"].([]any)
	if len(parts) != 1+2*3 {
		t.Fatalf("content parts = %d, expected 7", len(parts))
	}
	if strings.Contains(parts[0].(map[string]any)["text"].(string), cfg.Prompt) {
		t.Fatal("prompt duplicated in user content")
	}
	img := parts[2].(map[string]any)["image_url"].(map[string]any)
	if !strings.HasPrefix(img["url"].(string), "data:image/jpeg;base64,") {
		t.Errorf("图片应为 data URL")
	}
	if gotBody["stream"] != false {
		t.Error("stream 必须为 false")
	}
	if gotBody["temperature"] != 0.2 || gotBody["max_tokens"] != float64(128) {
		t.Errorf("采样参数异常: %v", gotBody)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if _, has := gotBody["seed"]; has {
		t.Error("不应发送 seed")
	}
	if _, has := gotBody["top_p"]; has {
		t.Error("不应发送 top_p")
	}
}

func TestNoKeyMeansNoAuthHeader(t *testing.T) {
	var auth = "sentinel"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte(okResponse("t")))
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	c := newTestClient(cfg)
	if _, jerr := c.Title(t.Context(), cfg.Prompt, frameSet(1)); jerr != nil {
		t.Fatal(jerr)
	}
	if auth != "" {
		t.Errorf("无 key 时不应发送 Authorization, got %q", auth)
	}
}

func TestUnauthorizedNoRetryAndGlobal(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	res, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIUnauthorized {
		t.Fatalf("应报 API_UNAUTHORIZED, got %+v", jerr)
	}
	if !jerr.Global {
		t.Error("鉴权失败应标记全局故障")
	}
	if calls.Load() != 1 {
		t.Errorf("401 不应重试, calls = %d", calls.Load())
	}
	if res.Attempts != 1 {
		t.Errorf("attempts = %d", res.Attempts)
	}
}

func TestRetryableStatusRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"message":"loading"}}`))
			return
		}
		w.Write([]byte(okResponse("第三次的标题")))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	res, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if res.Title != "第三次的标题" || res.Attempts != 3 {
		t.Errorf("应在第 3 次成功: %+v", res)
	}
}

func TestRetryExhausted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"busy"}}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	res, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIRateLimited {
		t.Fatalf("应报 API_RATE_LIMITED, got %+v", jerr)
	}
	if calls.Load() != 3 {
		t.Errorf("最多 3 次尝试, calls = %d", calls.Load())
	}
	if res.Attempts != 3 {
		t.Errorf("attempts = %d", res.Attempts)
	}
}

func TestBadRequestNoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIHTTPError || jerr.HTTPStatus != 400 {
		t.Fatalf("400 不重试, got %+v", jerr)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d", calls.Load())
	}
	if !strings.Contains(jerr.StderrTail, "invalid request") {
		t.Errorf("应保留响应摘要: %q", jerr.StderrTail)
	}
}

func TestRetryAfterTooLongStops(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIRateLimited {
		t.Fatalf("got %+v", jerr)
	}
	if !strings.Contains(jerr.Message, "停止重试") {
		t.Errorf("应注明停止原因: %s", jerr.Message)
	}
	if calls.Load() != 1 {
		t.Errorf("不应再重发: %d", calls.Load())
	}
}

func TestResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 4096))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	c.responseLimit = 1024
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIResponseTooLarge {
		t.Fatalf("应报 API_RESPONSE_TOO_LARGE, got %+v", jerr)
	}
}

func TestBodyBudgetDropsFrames(t *testing.T) {
	var images int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.UnmarshalRead(r.Body, &body)
		msgs := body["messages"].([]any)
		parts := msgs[1].(map[string]any)["content"].([]any)
		for _, p := range parts {
			if p.(map[string]any)["type"] == "image_url" {
				images++
			}
		}
		w.Write([]byte(okResponse("t")))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	c.bodyLimit = 2000 // 4 帧(每帧约 700B)必超,均匀删至 2 帧
	res, jerr := c.Title(t.Context(), "p", frameSet(4))
	if jerr != nil {
		t.Fatal(jerr)
	}
	if images != 2 { // 保留偶数下标 [0,2]
		t.Errorf("预算裁剪后应剩 2 帧, got %d", images)
	}
	if res.FramesSent != 2 {
		t.Errorf("FramesSent = %d", res.FramesSent)
	}
}

func TestBodyBudgetSingleFrameTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(okResponse("t")))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	c.bodyLimit = 16
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeRequestTooLarge {
		t.Fatalf("应报 REQUEST_TOO_LARGE, got %+v", jerr)
	}
}

func TestZeroFramesRejected(t *testing.T) {
	c := newTestClient(testConfig(t, "http://unused"))
	if _, jerr := c.Title(t.Context(), "p", nil); jerr == nil {
		t.Fatal("零帧不应调用模型")
	}
}

func TestTruncatedOutputRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"被截断的"},"finish_reason":"length"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeModelOutputTruncated {
		t.Fatalf("应报 MODEL_OUTPUT_TRUNCATED, got %+v", jerr)
	}
	if !strings.Contains(jerr.RawResponse, "finish_reason") || !strings.Contains(jerr.RawRequest, "messages") {
		t.Fatal("missing diagnostics")
	}
}

func TestToolCallsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"t","tool_calls":[{"id":"1"}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIResponseInvalid {
		t.Fatalf("应拒绝 tool_calls, got %+v", jerr)
	}
}

func TestNullToolCallsAccepted(t *testing.T) {
	// 网关(sub2api 等)常显式返回 "tool_calls": null,不应误判为工具调用。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"一个正常的中文标题","tool_calls":null},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	result, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr != nil {
		t.Fatalf("tool_calls 为 null 时应成功, got %+v", jerr)
	}
	if result.Title != "一个正常的中文标题" {
		t.Fatalf("标题不符, got %q", result.Title)
	}
}

func TestEmptyChoicesRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIResponseInvalid {
		t.Fatalf("应拒绝空 choices, got %+v", jerr)
	}
}

func TestBadJSONRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil || jerr.Code != domain.CodeAPIResponseInvalid {
		t.Fatalf("应报 API_RESPONSE_INVALID, got %+v", jerr)
	}
}

func TestRedirectDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(302)
			return
		}
		w.Write([]byte(okResponse("redirected")))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	// CheckRedirect 返回错误 → 传输层错误,绝不追随重定向
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil {
		t.Fatal("重定向应被拒绝")
	}
	if jerr.Code != domain.CodeAPIHTTPError && jerr.Code != domain.CodeAPITimeout {
		t.Fatalf("got %+v", jerr)
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Write([]byte(okResponse("late")))
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.TimeoutDuration = 80 * time.Millisecond
	c := newTestClient(cfg)
	_, jerr := c.Title(t.Context(), "p", frameSet(1))
	if jerr == nil {
		t.Fatal("超时应失败")
	}
	if jerr.Code != domain.CodeAPITimeout && jerr.Code != domain.CodeAPIHTTPError {
		t.Logf("超时错误码: %s", jerr.Code)
	}
}

func TestValidateTitle(t *testing.T) {
	valid := map[string]string{
		"  海边散步  ":          "海边散步",
		"\"海边散步\"":          "海边散步",
		"“海边散步”":            "海边散步",
		"「海边散步」":            "海边散步",
		"product demo 2024": "product demo 2024",
	}
	for in, want := range valid {
		got, jerr := ValidateTitle(in)
		if jerr != nil {
			t.Errorf("%q 应合法: %v", in, jerr)
			continue
		}
		if got != want {
			t.Errorf("%q → %q, 期望 %q", in, got, want)
		}
	}
	invalid := []string{
		"",
		"   ",
		"第一行\n第二行",
		"```json\n{\"t\":1}\n```",
		"1. 标题一\n2. 标题二",
	}
	for _, in := range invalid {
		if _, jerr := ValidateTitle(in); jerr == nil {
			t.Errorf("%q 应无效", in)
		}
	}
	// 这是解释:标题 是单行,协议上允许(只做单行验证,不做语义判断)
	if _, jerr := ValidateTitle("这是解释:标题"); jerr != nil {
		t.Errorf("单行文本应通过(不猜测语义): %v", jerr)
	}
}

func TestRetryBackoffValues(t *testing.T) {
	// k=1 → 2s;k=2 → 4s;上限 15s
	c := newTestClient(testConfig(t, "http://unused"))
	_ = c
	validate := func(k int, wantBase time.Duration) {
		base := time.Duration(min(15*time.Second, (1<<uint(k-1))*2*time.Second))
		if base != wantBase {
			t.Errorf("k=%d base=%v want %v", k, base, wantBase)
		}
	}
	validate(1, 2*time.Second)
	validate(2, 4*time.Second)
	validate(4, 15*time.Second)
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("3"); d != 3*time.Second {
		t.Errorf("got %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("got %v", d)
	}
	if d := parseRetryAfter("-5"); d != 0 {
		t.Errorf("got %v", d)
	}
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(future); d < 25*time.Second {
		t.Errorf("HTTP 日期应解析, got %v", d)
	}
}

func TestFormatTimestamp(t *testing.T) {
	if got := formatTimestamp(0); got != "00:00.000" {
		t.Errorf("got %s", got)
	}
	if got := formatTimestamp(83234 * time.Millisecond); got != "01:23.234" {
		t.Errorf("got %s", got)
	}
	if got := formatTimestamp(3661500 * time.Millisecond); got != "1:01:01.500" {
		t.Errorf("got %s", got)
	}
}

// Even when reusing a client, each video sends exactly its own prompt and images.
func TestConsecutiveVideosDoNotShareConversation(t *testing.T) {
	bodies := make(chan chatRequest, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatRequest
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		bodies <- body
		w.Write([]byte(okResponse("前一个视频的回答")))
	}))
	defer srv.Close()
	c := newTestClient(testConfig(t, srv.URL))
	for _, marker := range []string{"视频甲", "视频乙"} {
		path := filepath.Join(t.TempDir(), "frame.jpg")
		if err := os.WriteFile(path, []byte(marker), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Title(t.Context(), marker, []domain.Frame{{Path: path}}); err != nil {
			t.Fatal(err)
		}
		body := <-bodies
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
			t.Fatalf("history leaked: %+v", body.Messages)
		}
		if body.Messages[0].Content != marker {
			t.Fatal("wrong system context", body.Messages[0])
		}
		parts := body.Messages[1].Content.([]any)
		if len(parts) != 3 || parts[2].(map[string]any)["image_url"].(map[string]any)["url"] != dataURL(path) {
			t.Fatalf("wrong video context: %+v", parts)
		}
	}
}

func TestTenFramesAndRequestParameters(t *testing.T) {
	cfg := testConfig(t, "http://unused")
	cfg.Server.Temperature = 0.65
	cfg.Server.MaxTokens = 256
	c := newTestClient(cfg)
	frames := frameSet(10)
	defer os.RemoveAll(filepath.Dir(frames[0].Path))
	raw, count, err := c.buildBody("创作一个自然的标题，不要写图片描述", frames)
	if err != nil || count != 10 {
		t.Fatal(count, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["temperature"] != 0.65 || body["max_tokens"] != float64(256) || body["stream"] != false {
		t.Fatal("sampling not forwarded", body)
	}
	for _, key := range []string{"top_p", "top_k", "seed", "previous_response_id", "conversation_id"} {
		if _, ok := body[key]; ok {
			t.Fatal("unexpected parameter", key)
		}
	}
	messages := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatal("unexpected history")
	}
	parts := messages[1].(map[string]any)["content"].([]any)
	if len(parts) != 21 {
		t.Fatalf("expected preface + 10 timestamp/image pairs, got %d", len(parts))
	}
	for i, frame := range frames {
		text := parts[1+2*i].(map[string]any)
		image := parts[2+2*i].(map[string]any)
		if text["type"] != "text" || !strings.Contains(text["text"].(string), formatTimestamp(frame.Actual)) || image["type"] != "image_url" || image["image_url"].(map[string]any)["url"] != dataURL(frame.Path) {
			t.Fatalf("frame order wrong: %d", i)
		}
	}
}

func TestNamingFailureMarkerStopsWithoutRetry(t *testing.T) {
	for _, answer := range []string{"命名失败", "  “命名失败”  ", "命名失败。", "命名失败：无法生成标题"} {
		t.Run(answer, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				prompt := body["messages"].([]any)[0].(map[string]any)["content"].(string)
				if prompt != "自定义命名要求" {
					t.Error("system prompt not sent verbatim:", prompt)
				}
				fmt.Fprint(w, okResponse(answer))
			}))
			defer server.Close()
			result, err := newTestClient(testConfig(t, server.URL)).Title(t.Context(), "自定义命名要求", frameSet(1))
			if err == nil || err.Code != domain.CodeNamingFailed || err.Global || result.Title != "" || calls != 1 {
				t.Fatalf("result=%+v err=%+v calls=%d", result, err, calls)
			}
			if err.RawRequest == "" || !strings.Contains(err.RawResponse, "命名失败") {
				t.Fatal("missing failure diagnostics")
			}
		})
	}
}

func TestSuccessfulTitleKeepsSanitizedExchange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"response-123","choices":[{"message":{"content":"山间日出"},"finish_reason":"stop"}],"usage":{"total_tokens":123},"api_key":"private-key"}`)
	}))
	defer server.Close()
	cfg := testConfig(t, server.URL)
	cfg.Server.APIKey = "private-key"
	result, err := newTestClient(cfg).Title(t.Context(), "标题测试", frameSet(1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.RawResponse, "response-123") || !strings.Contains(result.RawResponse, "total_tokens") || strings.Contains(result.RawResponse, "private-key") || strings.Contains(result.RawRequest, "base64,") {
		t.Fatalf("unsafe/incomplete exchange: %+v", result)
	}
}
