package model

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
)

func TestVisionProbe(t *testing.T) {
	for _, mode := range []string{"passed", "mismatch", "error"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "error" {
					w.WriteHeader(429)
					w.Write([]byte(`{"error":{"message":"rate limited"}}`))
					return
				}
				var body struct {
					Messages []struct {
						Content jsontext.Value `json:"content"`
					} `json:"messages"`
				}
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				var parts []struct {
					ImageURL *struct {
						URL string `json:"url"`
					} `json:"image_url"`
				}
				if err := json.Unmarshal(body.Messages[1].Content, &parts); err != nil {
					t.Error(err)
				}
				var url string
				for _, p := range parts {
					if p.ImageURL != nil {
						url = p.ImageURL.URL
					}
				}
				raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/jpeg;base64,"))
				if err != nil {
					t.Error(err)
					return
				}
				img, err := jpeg.Decode(bytes.NewReader(raw))
				if err != nil {
					t.Error(err)
					return
				}
				names := []string{}
				for i := range 4 {
					red, green, blue, _ := img.At(80+i*160, 80).RGBA()
					name := "green"
					if red > green*2 && red > blue*2 {
						name = "red"
					} else if blue > red*2 && blue > green*2 {
						name = "blue"
					} else if red > 40000 && green > 40000 {
						name = "yellow"
					}
					names = append(names, name)
				}
				answer := strings.Join(names, ",")
				if mode == "mismatch" {
					answer = "unknown"
				}
				if err := json.MarshalWrite(w, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": answer}}}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			out, err := Probe(t.Context(), config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: server.URL, Model: "test", Timeout: "1s", MaxTokens: 1024}, 5*time.Second)
			if err != nil || out.Status != mode || calls != 1 {
				t.Fatalf("%+v %v calls=%d", out, err, calls)
			}
		})
	}
}

func TestVisionProbeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	started := time.Now()
	out, err := Probe(t.Context(), config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: server.URL, Model: "test", Timeout: "180s", MaxTokens: 1024}, 5*time.Second)
	if err != nil || out.Status != "error" || out.Error.Code != "api_timeout" {
		t.Fatalf("unexpected result: %+v %v", out, err)
	}
	if elapsed := time.Since(started); elapsed < 4*time.Second || elapsed > 7*time.Second {
		t.Fatalf("unexpected duration: %v", elapsed)
	}
	if _, err := Probe(t.Context(), config.ServerConfig{}, time.Second); err == nil {
		t.Fatal("unsupported probe timeout accepted")
	}
}

func TestVisionProbeIgnoresSavedRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		time.Sleep(1200 * time.Millisecond) // a model slower than the saved 1s request timeout
		if err := json.MarshalWrite(w, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": "unknown"}}}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	out, err := Probe(t.Context(), config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: server.URL, Model: "test", Timeout: "1s", MaxTokens: 1024}, 5*time.Second)
	if err != nil || out.Status != "mismatch" {
		t.Fatalf("probe should use its own timeout: %+v %v", out, err)
	}
}
