package config

import (
	"testing"
	"time"
)

func TestDeploymentRejectsModelConfiguration(t *testing.T) {
	for _, yaml := range []string{"server:\n  base_url: http://localhost/v1\n  model: m\n", "model: m\n", "api_key: secret\n", "temperature: 0.7\n"} {
		if _, err := loadTemp(t, yaml, "config.yaml"); err == nil {
			t.Fatalf("accepted retired model YAML %q", yaml)
		}
	}
}

func TestRuntimeModelValidation(t *testing.T) {
	base := ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://localhost:8080/v1/", Model: "vision", TimeoutMS: 180000, Temperature: 0, MaxTokens: 128}
	var cfg TaskConfig
	if err := cfg.ApplyModel(base, "prompt"); err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "http://localhost:8080/v1/chat/completions" || cfg.Server.Temperature != 0 || cfg.TimeoutDuration != 180*time.Second {
		t.Fatal("bad runtime values")
	}
	for _, u := range []string{"ftp://host", "http://user:pass@host/v1", "http://host/v1?key=secret", "http://host/v1#x", ""} {
		c := base
		c.BaseURL = u
		if cfg.ApplyModel(c, "p") == nil {
			t.Fatalf("accepted URL %s", u)
		}
	}
	for _, mutate := range []func(*ServerConfig){func(s *ServerConfig) { s.TimeoutMS = 0 }, func(s *ServerConfig) { s.TimeoutMS = 3600000 }, func(s *ServerConfig) { s.Temperature = 2.1 }, func(s *ServerConfig) { s.MaxTokens = 0 }, func(s *ServerConfig) { s.APIKey = "key\r\nInjected: value" }, func(s *ServerConfig) { s.Model = " " }, func(s *ServerConfig) { s.Provider = "bad" }, func(s *ServerConfig) { s.Protocol = "anthropic"; s.Temperature = 1.1 }} {
		c := base
		mutate(&c)
		if cfg.ApplyModel(c, "p") == nil {
			t.Fatal("accepted invalid setting")
		}
	}
}

func TestModelWorkers(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 4, 32, 33} {
		c := TaskConfig{}
		err := c.ApplyModel(ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: "http://localhost/v1", Model: "test", TimeoutMS: 10000, MaxTokens: 1024, Workers: n}, "title")
		if (err != nil) != (n < 0 || n > 32) {
			t.Fatalf("workers %d: %v", n, err)
		}
		if err == nil {
			want := n
			if want == 0 {
				want = 2
			}
			if c.Workers != want {
				t.Fatalf("workers not applied: %d", c.Workers)
			}
		}
	}
}
