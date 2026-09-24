package vision

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"video-rename/internal/config"
)

func TestAnthropicMultiImageSystemAndParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "claude-test-key" || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("bad endpoint or headers")
		}
		var body struct {
			System   string `json:"system"`
			Messages []struct {
				Role    string          `json:"role"`
				Content []anthropicPart `json:"content"`
			} `json:"messages"`
			Temperature float64 `json:"temperature"`
			MaxTokens   int     `json:"max_tokens"`
		}
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		if body.System != "标题要求" || len(body.Messages) != 1 || body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 21 || body.Temperature != 0.8 || body.MaxTokens != 1024 {
			t.Error("invalid payload")
		}
		for i, p := range body.Messages[0].Content {
			if i > 0 && i%2 == 0 {
				if p.Type != "image" || p.Source == nil || p.Source.Type != "base64" || p.Source.Data == "" {
					t.Error("missing image")
				}
			}
		}
		w.Write([]byte(`{"content":[{"type":"text","text":"山间晨雾"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()
	var cfg config.Config
	err := cfg.ApplyModel(config.ServerConfig{Provider: "claude", BaseURL: server.URL + "/v1", APIKey: "claude-test-key", Model: "vision", Timeout: "2s", Temperature: 0.8, MaxTokens: 1024}, "标题要求")
	if err != nil {
		t.Fatal(err)
	}
	result, jobErr := New(&cfg).Title(t.Context(), cfg.Prompt, frameSet(10))
	if jobErr != nil || result.Title != "山间晨雾" {
		t.Fatal(result, jobErr)
	}
	if _, _, err := parseAnthropic([]byte(`{"content":[{"type":"text","text":"半句"}],"stop_reason":"max_tokens"}`), 200); err == nil {
		t.Fatal("accepted truncated output")
	}
}
func TestOptionalTemperatureOmitted(t *testing.T) {
	cfg := testConfig(t, "http://localhost/v1")
	cfg.Server.OmitTemperature = true
	c := New(cfg)
	raw, _, err := c.buildBody("p", frameSet(1))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(raw, &body)
	if _, ok := body["temperature"]; ok {
		t.Fatal("temperature still sent")
	}
}

func TestOpenAIMaxCompletionTokens(t *testing.T) {
	cfg := testConfig(t, "http://localhost/v1")
	cfg.Server.Provider = "openai"
	c := New(cfg)
	raw, _, err := c.buildBody("p", frameSet(1))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(raw, &body)
	if body["max_completion_tokens"] != float64(128) {
		t.Fatal("wrong output token parameter")
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatal("deprecated parameter sent")
	}
}
