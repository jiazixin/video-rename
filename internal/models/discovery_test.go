package models

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
			out, err := Discover(t.Context(), cfg, 5*time.Second)
			if err != nil || len(out.Models) != 2 || out.Models[0].ID != "a-model" {
				t.Fatal(out, err)
			}
		})
	}
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	if _, err := Discover(t.Context(), config.ServerConfig{Provider: "custom", BaseURL: redirect.URL, APIKey: "secret"}, 5*time.Second); err == nil || hits != 0 {
		t.Fatal("followed redirect", err, hits)
	}
	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("secret in upstream error"))
	}))
	defer failure.Close()
	_, err := Discover(t.Context(), config.ServerConfig{Provider: "custom", BaseURL: failure.URL, APIKey: "secret"}, 5*time.Second)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("unsafe error", err)
	}
}

func TestDiscoverTimeout(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if streaming {
					w.Write([]byte(`{"data":[`))
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			started := time.Now()
			_, err := Discover(t.Context(), config.ServerConfig{Provider: "custom", BaseURL: server.URL}, 5*time.Second)
			if err == nil || !strings.Contains(err.Error(), "模型列表检测超时（5s）") {
				t.Fatalf("unexpected error: %v", err)
			}
			if elapsed := time.Since(started); elapsed < 4*time.Second || elapsed > 7*time.Second {
				t.Fatalf("unexpected duration: %v", elapsed)
			}
		})
	}
}
