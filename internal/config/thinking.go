package config

import (
	"fmt"
	"strings"
)

// ThinkingFields resolves provider-specific controls. Default deliberately sends nothing.
func (s ServerConfig) ThinkingFields() (map[string]any, error) {
	fields := map[string]any{}
	if s.Thinking == "" || s.Thinking == "default" {
		return fields, nil
	}
	if s.Thinking != "enabled" && s.Thinking != "disabled" {
		return nil, fmt.Errorf("思考模式必须为默认、开启或关闭")
	}
	enabled := s.Thinking == "enabled"
	adapter := s.ThinkingAdapter
	model := strings.ToLower(s.Model)
	if s.Provider != "custom" {
		adapter = ""
	}
	if adapter == "" || adapter == "auto" {
		switch s.Provider {
		case "deepseek", "zhipu", "zhipu-coding":
			adapter = "thinking"
		case "ollama":
			adapter = "effort"
		case "openai":
			switch {
			case strings.HasPrefix(model, "gpt-5.1"), strings.HasPrefix(model, "gpt-5.2"), strings.HasPrefix(model, "gpt-5.4"), strings.HasPrefix(model, "gpt-5.5"):
				if strings.Contains(model, "pro") || strings.Contains(model, "codex") || strings.Contains(model, "chat") {
					return nil, fmt.Errorf("此 OpenAI 型号尚未适配思考控制，请选择跟随模型默认")
				}
				adapter = "effort"
			case model == "gpt-5" || strings.HasPrefix(model, "gpt-5-"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
				if !enabled {
					return nil, fmt.Errorf("该型号不支持关闭思考，请使用默认或开启低强度思考")
				}
				adapter = "effort"
			default:
				return nil, fmt.Errorf("该 OpenAI 型号尚未适配思考控制，请选择跟随模型默认")
			}
		case "claude":
			if strings.Contains(model, "mythos") || strings.Contains(model, "fable") {
				if !enabled {
					return nil, fmt.Errorf("该 Claude 型号无法关闭思考")
				}
				adapter = "adaptive"
			} else if strings.Contains(model, "4-6") || strings.Contains(model, "4.6") || strings.Contains(model, "4-7") || strings.Contains(model, "4.7") || strings.Contains(model, "4-8") || strings.Contains(model, "4.8") || strings.HasPrefix(model, "claude-opus-5") || strings.HasPrefix(model, "claude-sonnet-5") {
				adapter = "adaptive"
			} else if strings.Contains(model, "4-5") || strings.Contains(model, "4.5") || strings.Contains(model, "3-7") || strings.Contains(model, "3.7") || strings.HasPrefix(model, "claude-sonnet-4") || strings.HasPrefix(model, "claude-opus-4") {
				adapter = "budget"
			} else {
				return nil, fmt.Errorf("该 Claude 型号尚未适配思考控制，请选择跟随模型默认")
			}
		case "custom":
			return nil, fmt.Errorf("自定义服务开启或关闭思考前，请选择服务支持的思考参数类型")
		default:
			return nil, fmt.Errorf("该服务不支持思考控制")
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
