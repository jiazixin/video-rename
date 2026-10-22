package config

import (
	"fmt"
	"strings"
)

// ThinkingFields resolves provider-specific controls. Default deliberately sends nothing.
func (s ServerConfig) ThinkingFields() (map[string]any, error) {
	if s.Thinking == "" || s.Thinking == "default" {
		return map[string]any{}, nil
	}
	if s.Thinking != "enabled" && s.Thinking != "disabled" {
		return nil, fmt.Errorf("思考模式必须为默认、开启或关闭")
	}
	enabled := s.Thinking == "enabled"
	adapter := s.ThinkingAdapter
	if s.Provider != "custom" {
		adapter = ""
	}
	if adapter == "" || adapter == "auto" {
		var err error
		if adapter, err = autoThinkingAdapter(s.Provider, strings.ToLower(s.Model), enabled); err != nil {
			return nil, err
		}
	}
	if s.Protocol == "anthropic" && adapter != "adaptive" && adapter != "budget" {
		return nil, fmt.Errorf("Anthropic 协议请选择自适应或固定预算思考参数")
	}
	if s.Protocol != "anthropic" && (adapter == "adaptive" || adapter == "budget") {
		return nil, fmt.Errorf("Claude 思考参数需要 Anthropic 协议")
	}
	effort := s.ThinkingEffort
	if effort == "" {
		effort = "low"
	}
	if effort != "low" && effort != "medium" && effort != "high" {
		return nil, fmt.Errorf("思考强度必须为低、中或高")
	}
	return s.thinkingRequestFields(adapter, enabled, effort)
}

// autoThinkingAdapter picks the request parameter family a known provider and
// model accept. model is lower-cased.
func autoThinkingAdapter(provider, model string, enabled bool) (string, error) {
	switch provider {
	case "deepseek", "zhipu", "zhipu-coding":
		return "thinking", nil
	case "ollama":
		return "effort", nil
	case "openai":
		return openAIThinkingAdapter(model, enabled)
	case "claude":
		return claudeThinkingAdapter(model, enabled)
	case "custom":
		return "", fmt.Errorf("自定义服务开启或关闭思考前，请选择服务支持的思考参数类型")
	default:
		return "", fmt.Errorf("该服务不支持思考控制")
	}
}

func openAIThinkingAdapter(model string, enabled bool) (string, error) {
	switch {
	case strings.HasPrefix(model, "gpt-5.1"), strings.HasPrefix(model, "gpt-5.2"), strings.HasPrefix(model, "gpt-5.4"), strings.HasPrefix(model, "gpt-5.5"):
		if strings.Contains(model, "pro") || strings.Contains(model, "codex") || strings.Contains(model, "chat") {
			return "", fmt.Errorf("此 OpenAI 型号尚未适配思考控制，请选择跟随模型默认")
		}
		return "effort", nil
	case model == "gpt-5" || strings.HasPrefix(model, "gpt-5-"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
		if !enabled {
			return "", fmt.Errorf("该型号不支持关闭思考，请使用默认或开启低强度思考")
		}
		return "effort", nil
	default:
		return "", fmt.Errorf("该 OpenAI 型号尚未适配思考控制，请选择跟随模型默认")
	}
}

func claudeThinkingAdapter(model string, enabled bool) (string, error) {
	containsAny := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(model, part) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("mythos", "fable"):
		if !enabled {
			return "", fmt.Errorf("该 Claude 型号无法关闭思考")
		}
		return "adaptive", nil
	case containsAny("4-6", "4.6", "4-7", "4.7", "4-8", "4.8") || strings.HasPrefix(model, "claude-opus-5") || strings.HasPrefix(model, "claude-sonnet-5"):
		return "adaptive", nil
	case containsAny("4-5", "4.5", "3-7", "3.7") || strings.HasPrefix(model, "claude-sonnet-4") || strings.HasPrefix(model, "claude-opus-4"):
		return "budget", nil
	default:
		return "", fmt.Errorf("该 Claude 型号尚未适配思考控制，请选择跟随模型默认")
	}
}

// thinkingRequestFields renders the request body fields of one adapter.
func (s ServerConfig) thinkingRequestFields(adapter string, enabled bool, effort string) (map[string]any, error) {
	fields := map[string]any{}
	switch adapter {
	case "thinking":
		fields["thinking"] = map[string]any{"type": s.Thinking}
	case "effort":
		if !enabled {
			effort = "none"
		}
		fields["reasoning_effort"] = effort
	case "template":
		fields["chat_template_kwargs"] = map[string]any{"enable_thinking": enabled}
	case "llama_cpp":
		budget := 0
		if enabled {
			if s.ThinkingBudget < 1 || s.ThinkingBudget >= s.MaxTokens {
				return nil, fmt.Errorf("llama.cpp 思考预算须至少 1，且小于最大输出 token")
			}
			budget = s.ThinkingBudget
		}
		fields["chat_template_kwargs"] = map[string]any{"enable_thinking": enabled}
		fields["reasoning_budget_tokens"] = budget
		fields["reasoning_format"] = "deepseek"
	case "adaptive":
		if enabled {
			fields["thinking"] = map[string]any{"type": "adaptive"}
			fields["output_config"] = map[string]any{"effort": effort}
		} else {
			fields["thinking"] = map[string]any{"type": "disabled"}
		}
	case "budget":
		if enabled {
			if s.ThinkingBudget < 1024 || s.ThinkingBudget >= s.MaxTokens {
				return nil, fmt.Errorf("Claude 思考预算须至少 1024，且小于最大输出 token")
			}
			fields["thinking"] = map[string]any{"type": "enabled", "budget_tokens": s.ThinkingBudget}
		} else {
			fields["thinking"] = map[string]any{"type": "disabled"}
		}
	default:
		return nil, fmt.Errorf("未知思考参数类型")
	}
	return fields, nil
}
