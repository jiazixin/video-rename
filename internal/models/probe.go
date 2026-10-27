package models

import (
	"context"
	"crypto/rand"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math/big"
	"os"
	"strings"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/vision"
)

// ProbeResult is the result of testing a model connection. Status is passed,
// mismatch or error; Error explains an error.
type ProbeResult struct {
	Status     string        `json:"status"`
	Answer     string        `json:"answer"`
	Expected   string        `json:"expected"`
	Error      domain.Record `json:"error,omitzero"`
	HTTPStatus int           `json:"http_status,omitzero"`
	ElapsedMS  int64         `json:"elapsed_ms"`
}

// Probe tests the model connection s within timeout, which must be 5, 10 or 15
// seconds.
func Probe(ctx context.Context, s config.ServerConfig, timeout time.Duration) (out ProbeResult, err error) {
	if timeout != 5*time.Second && timeout != 10*time.Second && timeout != 15*time.Second {
		return out, domain.InvalidField("timeout", "invalid_choice")
	}
	s.Timeout = timeout.String()
	var cfg config.TaskConfig
	if err = cfg.ApplyModel(s, ""); err != nil {
		return
	}
	names := []string{"red", "blue", "green", "yellow"}
	colors := []color.RGBA{{230, 25, 25, 255}, {25, 50, 230, 255}, {20, 180, 50, 255}, {245, 215, 15, 255}}
	order := []int{0, 1, 2, 3}
	for i := 3; i > 0; i-- {
		var n *big.Int
		n, err = rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return
		}
		j := int(n.Int64())
		order[i], order[j] = order[j], order[i]
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 640, 160))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	expected := []string{}
	for i, j := range order {
		draw.Draw(canvas, image.Rect(10+i*160, 20, 150+i*160, 140), image.NewUniform(colors[j]), image.Point{}, draw.Src)
		expected = append(expected, names[j])
	}
	f, e := os.CreateTemp("", "video-rename-probe-*.jpg")
	if e != nil {
		err = e
		return
	}
	defer os.Remove(f.Name())
	err = jpeg.Encode(f, canvas, &jpeg.Options{Quality: 90})
	closeErr := f.Close()
	if err != nil {
		return
	}
	if closeErr != nil {
		err = closeErr
		return
	}
	out.Expected = strings.Join(expected, ",")
	started := time.Now()
	client := vision.New(&cfg)
	defer client.Close()
	// The instruction is prompt text for the model, a recorded exception to
	// the error-code rule (README, code exceptions).
	answer, status, jobErr := client.Probe(ctx, "识别图片中从左到右的四个色块颜色。仅返回英文小写颜色名，以英文逗号分隔，不要空格和解释。", domain.Frame{Path: f.Name(), Width: 640, Height: 160})
	out.ElapsedMS = time.Since(started).Milliseconds()
	out.HTTPStatus = status
	out.Answer = answer
	if jobErr != nil {
		out.Status = "error"
		out.Error = jobErr.Record()
		return out, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(answer))
	normalized = strings.NewReplacer(" ", "", "，", ",", "。", "", ".", "", "\"", "").Replace(normalized)
	if normalized == out.Expected {
		out.Status = "passed"
	} else {
		out.Status = "mismatch"
	}
	return out, nil
}
