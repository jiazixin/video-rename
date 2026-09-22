package models

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				var parts []struct {
					ImageURL *struct {
						URL string `json:"url"`
					} `json:"image_url"`
				}
				json.Unmarshal(body.Messages[1].Content, &parts)
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
				for i := 0; i < 4; i++ {
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
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": answer}}}})
			}))
			defer server.Close()
			out, err := Probe(context.Background(), config.ServerConfig{Provider: "custom", Protocol: "openai", BaseURL: server.URL, Model: "test", Timeout: "5s", MaxTokens: 1024})
			if err != nil || out.Status != mode || calls != 1 {
				t.Fatalf("%+v %v calls=%d", out, err, calls)
			}
		})
	}
}
