package config

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

// ServerConfig is a runtime model snapshot, never a deployment YAML section.
type ServerConfig struct {
	Workers         int     `json:"workers"`
	Thinking        string  `json:"thinking"`
	ThinkingAdapter string  `json:"thinking_adapter"`
	ThinkingEffort  string  `json:"thinking_effort"`
	ThinkingBudget  int     `json:"thinking_budget"`
	Provider        string  `json:"provider"`
	Protocol        string  `json:"protocol"`
	BaseURL         string  `json:"base_url"`
	APIKey          string  `json:"-"`
	Model           string  `json:"model"`
	Timeout         string  `json:"timeout"`
	Temperature     float64 `json:"temperature"`
	MaxTokens       int     `json:"max_tokens"`
	OmitTemperature bool    `json:"omit_temperature"`
}

func ValidateConnection(s *ServerConfig) error {
	switch s.Provider {
	case "openai", "zhipu", "zhipu-coding", "deepseek", "claude", "ollama", "custom":
	default:
		return fmt.Errorf("请选择有效的服务类型")
	}
	if s.Protocol == "" {
		s.Protocol = "openai"
		if s.Provider == "claude" {
			s.Protocol = "anthropic"
		}
	}
	if s.Protocol != "openai" && s.Protocol != "anthropic" {
		return fmt.Errorf("协议必须为 OpenAI 或 Anthropic")
	}
	if s.Provider == "claude" && s.Protocol != "anthropic" || s.Provider != "claude" && s.Provider != "custom" && s.Protocol != "openai" {
		return fmt.Errorf("协议与服务类型不匹配")
	}
	u, err := url.Parse(strings.TrimSpace(s.BaseURL))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return fmt.Errorf("模型 URL 必须是无账号、查询参数和片段的 HTTP(S) 地址")
	}
	s.BaseURL = strings.TrimRight(u.String(), "/")
	if len(s.BaseURL) > 2048 || len(s.APIKey) > 8192 || strings.ContainsAny(s.APIKey, "\r\n") {
		return fmt.Errorf("URL 或密钥格式无效")
	}
	return nil
}
func (c *TaskConfig) ApplyModel(s ServerConfig, prompt string) error {
	if err := ValidateConnection(&s); err != nil {
		return err
	}
	s.Model = strings.TrimSpace(s.Model)
	if s.Model == "" || len(s.Model) > 256 {
		return fmt.Errorf("请填写模型名称")
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil || d < time.Second || d > 30*time.Minute {
		return fmt.Errorf("请求超时须在 1 秒至 30 分钟之间")
	}
	maxTemperature := 2.0
	if s.Protocol == "anthropic" {
		maxTemperature = 1
	}
	if math.IsNaN(s.Temperature) || math.IsInf(s.Temperature, 0) || s.Temperature < 0 || s.Temperature > maxTemperature {
		return fmt.Errorf("温度须在 0–%g 之间", maxTemperature)
	}
	if s.MaxTokens < 1 || s.MaxTokens > 32768 {
		return fmt.Errorf("输出上限须在 1–32768 之间")
	}
	if _, err := s.ThinkingFields(); err != nil {
		return err
	}
	// Reasoning models commonly disallow custom sampling parameters.
	if s.Thinking == "enabled" {
		s.OmitTemperature = true
	}
	if s.Workers == 0 {
		s.Workers = 2
	}
	if s.Workers < 1 || s.Workers > 32 {
		return fmt.Errorf("并发任务数须在 1–32 之间")
	}
	c.Workers = s.Workers
	c.Server = s
	c.TimeoutDuration = d
	c.Prompt = prompt
	c.Endpoint = s.BaseURL + "/chat/completions"
	if s.Protocol == "anthropic" {
		c.Endpoint = s.BaseURL + "/messages"
	}
	return nil
}
