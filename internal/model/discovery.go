package model

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

// AvailableModel is a model offered by a provider.
type AvailableModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Discovery lists the models offered by a provider.
type Discovery struct {
	Models    []AvailableModel `json:"models"`
	Truncated bool             `json:"truncated"`
}

// Discover only lists advertised models; it never sends a prompt or video.
func Discover(ctx context.Context, s config.ServerConfig, timeout time.Duration) (Discovery, error) {
	out := Discovery{Models: []AvailableModel{}}
	if timeout != 5*time.Second && timeout != 10*time.Second && timeout != 15*time.Second {
		return out, domain.InvalidField("timeout", "invalid_choice")
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
			return out, domain.Fail(domain.KindInvalid, domain.Record{Code: "discovery_request_invalid", Message: "model list request is invalid"})
		}
		req.Header.Set("Accept", "application/json")
		if s.Protocol == "anthropic" {
			req.Header.Set("anthropic-version", "2023-06-01")
			if s.APIKey != "" {
				req.Header.Set("x-api-key", s.APIKey.Reveal())
			}
		} else if s.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+s.APIKey.Reveal())
		}
		resp, err := client.Do(req)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_timeout", Message: "model list request timed out", Details: map[string]any{"timeout": timeout.String()}})
			}
			return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_unreachable", Message: "cannot connect to the model service"})
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		// The response is read-only; the bounded read above reports payload
		// failures, and closing cannot change an accepted response.
		resp.Body.Close()
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_timeout", Message: "model list request timed out", Details: map[string]any{"timeout": timeout.String()}})
			}
			return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_read_failed", Message: "read model list"})
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_http_error", Message: "model list endpoint returned an error status", Details: map[string]any{"status": resp.StatusCode}})
		}
		if len(raw) > 1<<20 {
			return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_too_large", Message: "model list response exceeds 1 MiB"})
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
			return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_invalid", Message: "service returned no valid model list"})
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
			return out, domain.Fail(domain.KindUnavailable, domain.Record{Code: "discovery_cursor_invalid", Message: "model list page cursor is invalid"})
		}
		after = data.LastID
		if page == 9 {
			out.Truncated = true
		}
	}
	slices.SortFunc(out.Models, func(a, b AvailableModel) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}
