package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestThinkingControls(t *testing.T) {
	for _, tc := range []struct {
		provider, protocol, model, mode, adapter, want string
		budget, max                                    int
		bad                                            bool
	}{
		{"deepseek", "openai", "deepseek-flash", "disabled", "", `"type":"disabled"`, 0, 1024, false},
		{"zhipu", "openai", "glm-4.6v", "enabled", "", `"type":"enabled"`, 0, 8192, false},
		{"ollama", "openai", "qwen3", "disabled", "", `"reasoning_effort":"none"`, 0, 1024, false},
		{"openai", "openai", "gpt-5.1", "enabled", "", `"reasoning_effort":"low"`, 0, 8192, false},
		{"openai", "openai", "o3", "disabled", "", "", 0, 1024, true},
		{"openai", "openai", "gpt-4o", "enabled", "", "", 0, 1024, true},
		{"claude", "anthropic", "claude-sonnet-4-6", "enabled", "", `"type":"adaptive"`, 0, 8192, false},
		{"claude", "anthropic", "claude-sonnet-4-5", "enabled", "", `"budget_tokens":1024`, 1024, 4096, false},
		{"claude", "anthropic", "claude-sonnet-4-5", "enabled", "", "", 1024, 1024, true},
		{"claude", "anthropic", "claude-mythos-5", "disabled", "", "", 0, 8192, true},
		{"custom", "openai", "local", "enabled", "", "", 0, 8192, true},
		{"custom", "openai", "local", "disabled", "template", `"enable_thinking":false`, 0, 1024, false},
		{"custom", "openai", "local", "default", "", "{}", 0, 1024, false},
	} {
		t.Run(tc.provider+tc.model+tc.mode, func(t *testing.T) {
			s := ServerConfig{Provider: tc.provider, Protocol: tc.protocol, Model: tc.model, Thinking: tc.mode, ThinkingAdapter: tc.adapter, ThinkingBudget: tc.budget, MaxTokens: tc.max}
			fields, err := s.ThinkingFields()
			if (err != nil) != tc.bad {
				t.Fatalf("%v", err)
			}
			if !tc.bad {
				raw, _ := json.Marshal(fields)
				if !strings.Contains(string(raw), tc.want) {
					t.Fatal(string(raw))
				}
			}
		})
	}
}
