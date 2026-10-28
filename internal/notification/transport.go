package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"video-rename/internal/domain"
)

type message struct {
	credentials       Credentials
	kind, title, body string
}

func request(m message) (string, any, error) {
	c, kind, title, body := m.credentials, m.kind, m.title, m.body
	endpoint := c.Endpoint.Reveal()
	content := title + "\n\n" + body
	switch kind {
	case "wecom":
		return endpoint, map[string]any{"msgtype": "text", "text": map[string]string{"content": content}}, nil
	case "dingtalk":
		if c.Secret != "" {
			u, err := url.Parse(endpoint)
			if err != nil {
				// URL parse errors include the credential-bearing URL itself.
				return "", nil, domain.InvalidField("endpoint", "invalid_url")
			}
			timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(c.Secret.Reveal()))
			// hash.Hash.Write always succeeds and returns no error.
			mac.Write([]byte(timestamp + "\n" + c.Secret.Reveal()))
			q := u.Query()
			q.Set("timestamp", timestamp)
			q.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			u.RawQuery = q.Encode()
			endpoint = u.String()
		}
		return endpoint, map[string]any{"msgtype": "text", "text": map[string]string{"content": content}}, nil
	case "telegram":
		return "https://api.telegram.org/bot" + c.Token.Reveal() + "/sendMessage", map[string]any{"chat_id": c.ChatID, "text": content}, nil
	case "bark":
		// Use the JSON API rather than the legacy /key[/title/body] route.
		u, err := url.Parse(endpoint)
		if err != nil {
			return "", nil, domain.InvalidField("endpoint", "invalid_url")
		}
		path := strings.TrimRight(u.Path, "/")
		prefix, deviceKey, _ := strings.CutLast(path, "/")
		u.Path = prefix + "/push"
		u.RawPath = ""
		q := u.Query()
		q.Del("title")
		q.Del("body")
		q.Del("device_key")
		u.RawQuery = q.Encode()
		return u.String(), map[string]string{"device_key": deviceKey, "title": title, "body": body, "group": "video-rename", "isArchive": "1"}, nil
	default:
		return endpoint, map[string]string{"event": "batch.finished", "title": title, "body": body}, nil
	}
}
func sendMessage(ctx context.Context, m message) error {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return deliver(ctx, client, m)
}
func deliver(ctx context.Context, client *http.Client, m message) error {
	c, kind := m.credentials, m.kind
	if kind == "bark" {
		if err := validate(kind, c); err != nil {
			return err
		}
	}
	endpoint, payload, err := request(m)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonNotificationFailed, Message: "encode notification", Details: map[string]any{"reason": "encode"}})
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonNotificationFailed, Message: "notification endpoint is invalid", Details: map[string]any{"reason": "invalid_endpoint"}})
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonNotificationFailed, Message: "notification connection failed or timed out", Details: map[string]any{"reason": "unreachable"}})
	}
	// The bounded read reports response failures; closing a read-only response
	// does not change whether the remote service accepted this delivery.
	defer res.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonNotificationFailed, Message: "notification response unreadable or too large", Details: map[string]any{"reason": "response_unreadable"}})
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonNotificationFailed, Message: "notification service returned an error status", Details: map[string]any{"reason": "http_status", "status": res.StatusCode}})
	}
	if kind == "webhook" {
		return nil
	}
	var answer struct {
		ErrCode *int  `json:"errcode"`
		OK      *bool `json:"ok"`
		Code    *int  `json:"code"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonNotificationFailed, Message: "notification response is malformed", Details: map[string]any{"reason": "response_invalid"}})
	}
	switch kind {
	case "telegram":
		if answer.OK != nil && *answer.OK {
			return nil
		}
	case "bark":
		if answer.Code != nil && *answer.Code == 200 {
			return nil
		}
	default:
		if answer.ErrCode != nil && *answer.ErrCode == 0 {
			return nil
		}
	}
	return domain.Fail(domain.KindUnavailable, domain.Failure{Reason: domain.ReasonNotificationFailed, Message: "notification service rejected the message", Details: map[string]any{"reason": "rejected"}})
}
