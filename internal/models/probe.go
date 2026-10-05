package models

import (
	"context"
	"crypto/rand"
	"fmt"
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

type ProbeResult struct {
	Status     string `json:"status"`
	Answer     string `json:"answer"`
	Expected   string `json:"expected"`
	Message    string `json:"message"`
	Code       string `json:"code,omitempty"`
	HTTPStatus int    `json:"http_status,omitzero"`
	ElapsedMS  int64  `json:"elapsed_ms"`
}

func Probe(ctx context.Context, s config.ServerConfig, timeout time.Duration) (out ProbeResult, err error) {
	if timeout != 5*time.Second && timeout != 10*time.Second && timeout != 15*time.Second {
		return out, fmt.Errorf("图片识别测试超时只能选择 5s、10s 或 15s")
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
	answer, status, jobErr := client.Probe(ctx, "识别图片中从左到右的四个色块颜色。仅返回英文小写颜色名，以英文逗号分隔，不要空格和解释。", domain.Frame{Path: f.Name(), Width: 640, Height: 160})
	out.ElapsedMS = time.Since(started).Milliseconds()
	out.HTTPStatus = status
	out.Answer = answer
	if jobErr != nil {
		out.Status = "error"
		out.Code = string(jobErr.Code)
		out.Message = jobErr.Message
		return out, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(answer))
	normalized = strings.NewReplacer(" ", "", "，", ",", "。", "", ".", "", "\"", "").Replace(normalized)
	if normalized == out.Expected {
		out.Status = "passed"
		out.Message = "测试图片识别正确"
	} else {
		out.Status = "mismatch"
		out.Message = "图片请求成功，但识别结果不符合预期；不能据此确认图片识别能力"
	}
	return out, nil
}
