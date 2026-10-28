package config

import (
	"math"
	"net/url"
	"strings"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/secret"
)

// ServerConfig is a runtime model snapshot, never a deployment YAML section.
type ServerConfig struct {
	Workers         int          `json:"workers"`
	Thinking        string       `json:"thinking"`
	ThinkingAdapter string       `json:"thinking_adapter"`
	ThinkingEffort  string       `json:"thinking_effort"`
	ThinkingBudget  int          `json:"thinking_budget"`
	Provider        string       `json:"provider"`
	Protocol        string       `json:"protocol"`
	BaseURL         string       `json:"base_url"`
	APIKey          secret.Value `json:"-"`
	Model           string       `json:"model"`
	TimeoutMS       int64        `json:"timeout_ms"`
	Temperature     float64      `json:"temperature"`
	MaxTokens       int          `json:"max_tokens"`
	OmitTemperature bool         `json:"omit_temperature"`
}

// ValidateConnection checks the provider, protocol and endpoint settings of s.
func ValidateConnection(s *ServerConfig) error {
	switch s.Provider {
	case "openai", "zhipu", "zhipu-coding", "deepseek", "claude", "ollama", "custom":
	default:
		return domain.InvalidField("provider", "invalid_choice")
	}
	if s.Protocol == "" {
		s.Protocol = "openai"
		if s.Provider == "claude" {
			s.Protocol = "anthropic"
		}
	}
	if s.Protocol != "openai" && s.Protocol != "anthropic" {
		return domain.InvalidField("protocol", "invalid_choice")
	}
	if s.Provider == "claude" && s.Protocol != "anthropic" || s.Provider != "claude" && s.Provider != "custom" && s.Protocol != "openai" {
		return domain.InvalidField("protocol", "provider_mismatch")
	}
	u, err := url.Parse(strings.TrimSpace(s.BaseURL))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return domain.InvalidField("base_url", "invalid_url")
	}
	s.BaseURL = strings.TrimRight(u.String(), "/")
	if len(s.BaseURL) > 2048 {
		return domain.InvalidField("base_url", "too_long")
	}
	if len(s.APIKey) > 8192 || strings.ContainsAny(s.APIKey.Reveal(), "\r\n") {
		return domain.InvalidField("api_key", "invalid")
	}
	return nil
}

// ApplyModel copies the model settings s and prompt into the task configuration
// after validating them.
func (c *TaskConfig) ApplyModel(s ServerConfig, prompt string) error {
	if err := ValidateConnection(&s); err != nil {
		return err
	}
	s.Model = strings.TrimSpace(s.Model)
	if s.Model == "" || len(s.Model) > 256 {
		return domain.InvalidField("model", "required")
	}
	if s.TimeoutMS < time.Second.Milliseconds() || s.TimeoutMS > (30*time.Minute).Milliseconds() {
		return domain.InvalidField("timeout_ms", "out_of_range")
	}
	d := time.Duration(s.TimeoutMS) * time.Millisecond
	maxTemperature := 2.0
	if s.Protocol == "anthropic" {
		maxTemperature = 1
	}
	if math.IsNaN(s.Temperature) || math.IsInf(s.Temperature, 0) || s.Temperature < 0 || s.Temperature > maxTemperature {
		if maxTemperature == 1 {
			return domain.InvalidField("temperature", "out_of_range_anthropic")
		}
		return domain.InvalidField("temperature", "out_of_range")
	}
	if s.MaxTokens < 1 || s.MaxTokens > 32768 {
		return domain.InvalidField("max_tokens", "out_of_range")
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
		return domain.InvalidField("workers", "out_of_range")
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
