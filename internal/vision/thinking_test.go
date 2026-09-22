package vision

import (
	"encoding/json"
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
			for k := range expected {
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
