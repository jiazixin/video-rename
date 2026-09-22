package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"video-rename/internal/config"
)

func TestDiscoverProtocolsAndRedirectProtection(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					t.Error(r.URL.Path)
				}
				if protocol == "anthropic" {
					if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") == "" {
						t.Error("missing anthropic auth")
					}
				} else if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("missing bearer")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"a-model"}]}`))
			}))
			defer server.Close()
			cfg := config.ServerConfig{Provider: "custom", Protocol: protocol, BaseURL: server.URL + "/v1", APIKey: "test-key"}
			out, err := Discover(context.Background(), cfg)
			if err != nil || len(out.Models) != 2 || out.Models[0].ID != "a-model" {
				t.Fatal(out, err)
			}
		})
	}
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	if _, err := Discover(context.Background(), config.ServerConfig{Provider: "custom", BaseURL: redirect.URL, APIKey: "secret"}); err == nil || hits != 0 {
		t.Fatal("followed redirect", err, hits)
	}
	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("secret in upstream error"))
	}))
	defer failure.Close()
	_, err := Discover(context.Background(), config.ServerConfig{Provider: "custom", BaseURL: failure.URL, APIKey: "secret"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("unsafe error", err)
	}
}
