package vision

import (
	"encoding/json/v2"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"video-rename/internal/config"
)

func TestThinkingRequestBody(t *testing.T) {
	for _, provider := range []string{"deepseek", "zhipu", "openai", "ollama", "claude", "custom"} {
		t.Run(provider, func(t *testing.T) {
			cfg := testConfig(t, "http://localhost")
			cfg.Server.Provider = provider
			cfg.Server.Thinking = "enabled"
			cfg.Server.ThinkingEffort = "low"
			cfg.Server.MaxTokens = 8192
			cfg.Server.Model = "gpt-5.1"
			cfg.Server.Protocol = "openai"
			if provider == "claude" {
				cfg.Server.Protocol = "anthropic"
				cfg.Server.Model = "claude-sonnet-4-6"
			}
			if provider == "custom" {
				cfg.Server.ThinkingAdapter = "template"
			}
			client := New(cfg)
			body, _, err := client.buildBody("title", frameSet(1))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			json.Unmarshal(body, &fields)
			expected, e := cfg.Server.ThinkingFields()
			if e != nil {
				t.Fatal(e)
			}
			for k := range maps.Keys(expected) {
				if fields[k] == nil {
					t.Fatalf("missing %s", k)
				}
			}
			if _, ok := fields["temperature"]; ok {
				t.Fatal("temperature sent with thinking")
			}
			client.settings = config.ServerConfig{}
			client.omitTemperature = false
			body, _, err = client.buildBody("title", frameSet(1))
			if err != nil {
				t.Fatal(err)
			}
			fields = nil
			json.Unmarshal(body, &fields)
			for _, k := range []string{"thinking", "reasoning_effort", "chat_template_kwargs"} {
				if fields[k] != nil {
					t.Fatal("default sent control", k)
				}
			}
		})
	}
}

func TestLlamaThinkingRequestBody(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "default"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t, "http://localhost")
			cfg.Server.Provider = "custom"
			cfg.Server.Protocol = "openai"
			cfg.Server.Thinking = mode
			cfg.Server.ThinkingAdapter = "llama_cpp"
			cfg.Server.ThinkingBudget = 512
			cfg.Server.MaxTokens = 2048
			body, _, err := New(cfg).buildBody("title", frameSet(1))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["max_tokens"] != float64(2048) {
				t.Fatalf("total budget changed: %s", body)
			}
			if mode == "default" {
				for _, key := range []string{"reasoning_budget_tokens", "reasoning_format", "chat_template_kwargs"} {
					if _, ok := fields[key]; ok {
						t.Fatalf("default sends %s", key)
					}
				}
				return
			}
			want := float64(0)
			if mode == "enabled" {
				want = 512
			}
			if fields["reasoning_budget_tokens"] != want || fields["reasoning_format"] != "deepseek" {
				t.Fatalf("wrong reasoning fields: %s", body)
			}
			kwargs, ok := fields["chat_template_kwargs"].(map[string]any)
			if !ok || kwargs["enable_thinking"] != (mode == "enabled") {
				t.Fatalf("wrong thinking toggle: %s", body)
			}
		})
	}
}

func TestLlamaReasoningIsNotTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"reasoning_content":"这段思考不应出现在文件名中", "content":"山间日出"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	cfg := testConfig(t, srv.URL)
	cfg.Server.Provider = "custom"
	cfg.Server.Protocol = "openai"
	cfg.Server.Thinking = "enabled"
	cfg.Server.ThinkingAdapter = "llama_cpp"
	cfg.Server.ThinkingBudget = 512
	cfg.Server.MaxTokens = 2048
	result, err := newTestClient(cfg).Title(t.Context(), "p", frameSet(1))
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "山间日出" {
		t.Fatalf("reasoning leaked into title: %q", result.Title)
	}
}
