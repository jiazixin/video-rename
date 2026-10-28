package app

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"video-rename/internal/config"
	modelstore "video-rename/internal/model"
)

type pipelineFixture struct {
	model string
	reply func(context.Context, string) (string, error)
}

// installPipelineFixture exercises the production probe/extract/model path
// using only disposable files, executable fixtures and a local HTTP server.
func installPipelineFixture(t *testing.T, a *App, fixture pipelineFixture) {
	t.Helper()
	tools := t.TempDir()
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 64, 36)), nil); err != nil {
		t.Fatal(err)
	}
	frame := filepath.Join(tools, "frame.jpg")
	if err := os.WriteFile(frame, jpegBytes.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"ffprobe": "#!/bin/sh\nprintf '%s\\n' '{\"format\":{\"duration\":\"1\"},\"streams\":[{\"index\":0,\"codec_type\":\"video\",\"width\":64,\"height\":36,\"sample_aspect_ratio\":\"1:1\"}]}'\n",
		"ffmpeg":  "#!/bin/sh\nfor output do :; done\nexec /bin/cp \"$VIDEO_RENAME_TEST_FRAME\" \"$output\"\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	t.Setenv("VIDEO_RENAME_TEST_FRAME", frame)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, 2<<20), &request); err != nil {
			http.Error(w, "invalid model request", http.StatusBadRequest)
			return
		}
		title := "fixture-title"
		if fixture.reply != nil {
			var err error
			title, err = fixture.reply(r.Context(), request.Model)
			if err != nil {
				http.Error(w, "fixture rejected model request", http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.MarshalWrite(w, map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"content": title}, "finish_reason": "stop",
		}}}); err != nil && r.Context().Err() == nil {
			t.Errorf("write model fixture response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	store := modelstore.New(a.db)
	profile, err := store.Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	profile.ServerConfig = config.ServerConfig{
		Provider: "custom", Protocol: "openai", BaseURL: server.URL + "/v1",
		Model: fixture.model, TimeoutMS: 10000, MaxTokens: 128,
	}
	if _, err := store.Save(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
}
