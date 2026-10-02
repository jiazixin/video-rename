package vision

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"video-rename/internal/domain"
)

func TestUnreadableFramesNeverReachModel(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		for _, broken := range []string{"missing", "empty", "directory"} {
			t.Run(protocol+"/"+broken, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					fmt.Fprint(w, okResponse("没有有效画面仍然返回的标题"))
				}))
				defer server.Close()
				dir := t.TempDir()
				path := filepath.Join(dir, "frame.jpg")
				if broken == "empty" {
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
				} else if broken == "directory" {
					path = dir
				}
				cfg := testConfig(t, server.URL)
				cfg.Server.Protocol = protocol
				client := New(cfg)
				defer client.Close()
				result, err := client.TitleOnce(t.Context(), "p", []domain.Frame{{Path: path}})
				if err == nil || err.Code != domain.CodeExtractFailed || calls.Load() != 0 || result.Attempts != 0 {
					t.Fatalf("broken screenshot reached model: result=%+v error=%v calls=%d", result, err, calls.Load())
				}
			})
		}
	}
}

func TestRetryAfterDoesNotOverflow(t *testing.T) {
	for _, raw := range []string{"9223372036", "9223372037", "9223372036854775807", "999999999999999999999999999999"} {
		if got := parseRetryAfter(raw); got != 9223372036*time.Second {
			t.Fatalf("Retry-After %s wrapped: %s", raw, got)
		}
	}
}

func TestCloseReleasesIdleConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, okResponse("正常标题"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.Start()
	defer server.Close()
	path := filepath.Join(t.TempDir(), "frame.jpg")
	if err := os.WriteFile(path, []byte("frame"), 0600); err != nil {
		t.Fatal(err)
	}
	client := New(testConfig(t, server.URL))
	defer client.Close()
	if _, err := client.TitleOnce(t.Context(), "p", []domain.Frame{{Path: path}}); err != nil {
		t.Fatal(err)
	}
	client.Close()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("inference client retained idle socket after Close")
	}
}

func TestErrorRedaction(t *testing.T) {
	const secret = "synthetic-review-secret-123"
	const prompt = "这是私密业务提示词\n第二行"
	for _, status := range []int{200, 400, 401} {
		for _, message := range []string{secret, "Authorization: Bearer unknown-token", prompt, "data:image/jpeg;base64,ABCDEF", strings.Repeat("x", 2040) + secret, "\"messages\":[{\"content\":\"private\"}]"} {
			t.Run(fmt.Sprintf("%d/%d", status, len(message)), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					_ = json.MarshalWrite(w, map[string]any{"error": map[string]string{"message": message}})
				}))
				defer srv.Close()
				cfg := testConfig(t, srv.URL)
				cfg.Server.APIKey = secret
				_, err := newTestClient(cfg).Title(t.Context(), prompt, frameSet(1))
				if err == nil {
					t.Fatal("应返回错误")
				}
				for _, sensitive := range []string{secret, prompt, "unknown-token", "ABCDEF", "private"} {
					if strings.Contains(err.Error(), sensitive) {
						t.Fatalf("错误包含敏感内容: %s", err.Error())
					}
				}
				if len(err.StderrTail) > 2048 || len(err.Message) > 2048 {
					t.Fatal("摘要超限")
				}
			})
		}
	}
}

func TestErrorJSONEscapedSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"\u0073ecret-value"},"headers":{"Authorization":"must-not-log"}}`)
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.Server.APIKey = "secret-value"
	_, err := newTestClient(cfg).Title(t.Context(), "p", frameSet(1))
	if err == nil || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "must-not-log") {
		t.Fatalf("未过滤 JSON 转义或额外字段: %v", err)
	}
}

func TestResponseBodyTimeoutRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(250 * time.Millisecond)
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.TimeoutDuration = 60 * time.Millisecond
	res, err := newTestClient(cfg).Title(t.Context(), "prompt", frameSet(1))
	if calls.Load() != 3 || res.Attempts != 3 || err == nil || err.Code != domain.CodeAPITimeout {
		t.Fatalf("响应头之后超时应按超时重试，实际 calls=%d attempts=%d error=%v", calls.Load(), res.Attempts, err)
	}
}

func TestResponseBodyDisconnectRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Content-Length", "100")
			fmt.Fprint(w, "{")
			return
		}
		fmt.Fprint(w, okResponse("最终标题"))
	}))
	defer srv.Close()
	res, err := newTestClient(testConfig(t, srv.URL)).Title(t.Context(), "p", frameSet(1))
	if err != nil || res.Attempts != 3 || res.Title != "最终标题" {
		t.Fatalf("重连失败: %+v %v", res, err)
	}
}

func TestResponseBodyCancellationDoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		cancel()
	}))
	defer srv.Close()
	res, err := newTestClient(testConfig(t, srv.URL)).Title(ctx, "p", frameSet(1))
	if err == nil || err.Code != domain.CodeCancelled || res.Attempts != 1 || calls.Load() != 1 {
		t.Fatalf("取消错误: %+v %v calls=%d", res, err, calls.Load())
	}
}

func TestResponseBodyDisconnectKeepsAuthFailure(t *testing.T) {
	for _, status := range []int{400, 401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(status)
				fmt.Fprint(w, "{")
			}))
			defer srv.Close()
			res, err := newTestClient(testConfig(t, srv.URL)).Title(t.Context(), "p", frameSet(1))
			if err == nil || err.HTTPStatus != status || calls.Load() != 1 || res.Attempts != 1 {
				t.Fatalf("状态策略丢失: %+v %v", res, err)
			}
			if status != 400 && (!err.Global || err.Code != domain.CodeAPIUnauthorized) {
				t.Fatal(err)
			}
		})
	}
}
