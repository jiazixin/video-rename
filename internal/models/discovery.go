package models

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
	"video-rename/internal/config"
)

type AvailableModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Discovery struct {
	Models    []AvailableModel `json:"models"`
	Truncated bool             `json:"truncated"`
}

// Discover only lists advertised models; it never sends a prompt or video.
func Discover(ctx context.Context, s config.ServerConfig, timeout time.Duration) (Discovery, error) {
	out := Discovery{Models: []AvailableModel{}}
	if timeout != 5*time.Second && timeout != 10*time.Second && timeout != 15*time.Second {
		return out, fmt.Errorf("检测超时只能选择 5s、10s 或 15s")
	}
	if err := config.ValidateConnection(&s); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	seen := map[string]bool{}
	after := ""
	for page := range 10 {
		endpoint := s.BaseURL + "/models"
		if s.Protocol == "anthropic" {
			endpoint += "?limit=100"
			if after != "" {
				endpoint += "&after_id=" + url.QueryEscape(after)
			}
		}
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return out, fmt.Errorf("模型检测请求无效")
		}
		req.Header.Set("Accept", "application/json")
		if s.Protocol == "anthropic" {
			req.Header.Set("anthropic-version", "2023-06-01")
			if s.APIKey != "" {
				req.Header.Set("x-api-key", s.APIKey)
			}
		} else if s.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+s.APIKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return out, fmt.Errorf("模型列表检测超时（%s），请重试或检查服务", timeout)
			}
			return out, fmt.Errorf("无法连接模型服务，请检查地址、网络和超时")
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		resp.Body.Close()
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return out, fmt.Errorf("模型列表检测超时（%s），请重试或检查服务", timeout)
			}
			return out, fmt.Errorf("读取模型列表失败")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return out, fmt.Errorf("模型列表接口返回 HTTP %d，请检查 URL、密钥及服务是否支持 /models", resp.StatusCode)
		}
		if len(raw) > 1<<20 {
			return out, fmt.Errorf("模型列表响应超过 1 MiB 上限")
		}
		var data struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"display_name"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Data == nil {
			return out, fmt.Errorf("服务未返回有效的模型列表；可手动填写模型名称")
		}
		for _, m := range data.Data {
			if m.ID == "" || len(m.ID) > 256 || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			if len(m.Name) > 256 {
				m.Name = ""
			}
			out.Models = append(out.Models, AvailableModel{m.ID, m.Name})
			if len(out.Models) >= 1000 {
				out.Truncated = true
				break
			}
		}
		if !data.HasMore || s.Protocol != "anthropic" || out.Truncated {
			break
		}
		if data.LastID == "" || data.LastID == after {
			return out, fmt.Errorf("模型列表分页游标无效")
		}
		after = data.LastID
		if page == 9 {
			out.Truncated = true
		}
	}
	slices.SortFunc(out.Models, func(a, b AvailableModel) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}
