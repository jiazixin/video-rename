package notifications

import (
	"bytes"
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

func request(c Credentials, kind, title, body string) (string, any) {
	endpoint := c.Endpoint
	content := title + "\n\n" + body
	switch kind {
	case "wecom":
		return endpoint, map[string]any{"msgtype": "text", "text": map[string]string{"content": content}}
	case "dingtalk":
		if c.Secret != "" {
			u, _ := url.Parse(endpoint)
			timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(c.Secret))
			mac.Write([]byte(timestamp + "\n" + c.Secret))
			q := u.Query()
			q.Set("timestamp", timestamp)
			q.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			u.RawQuery = q.Encode()
			endpoint = u.String()
		}
		return endpoint, map[string]any{"msgtype": "text", "text": map[string]string{"content": content}}
	case "telegram":
		return "https://api.telegram.org/bot" + c.Token + "/sendMessage", map[string]any{"chat_id": c.ChatID, "text": content}
	case "bark":
		// Use the JSON API rather than the legacy /key[/title/body] route.
		u, _ := url.Parse(endpoint)
		path := strings.TrimRight(u.Path, "/")
		prefix, deviceKey, _ := strings.CutLast(path, "/")
		u.Path = prefix + "/push"
		u.RawPath = ""
		q := u.Query()
		q.Del("title")
		q.Del("body")
		q.Del("device_key")
		u.RawQuery = q.Encode()
		return u.String(), map[string]string{"device_key": deviceKey, "title": title, "body": body, "group": "video-rename", "isArchive": "1"}
	default:
		return endpoint, map[string]string{"event": "batch.finished", "title": title, "body": body}
	}
}
func sendMessage(c Credentials, kind, title, body string) error {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return deliver(client, c, kind, title, body)
}
func deliver(client *http.Client, c Credentials, kind, title, body string) error {
	if kind == "bark" {
		if err := validate(kind, c); err != nil {
			return err
		}
	}
	endpoint, payload := request(c, kind, title, body)
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.Fail(domain.KindInternal, "notification_failed", "encode notification", "reason", "encode")
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return domain.Fail(domain.KindInvalid, "notification_failed", "notification endpoint is invalid", "reason", "invalid_endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return domain.Fail(domain.KindUnavailable, "notification_failed", "notification connection failed or timed out", "reason", "unreachable")
	}
	defer res.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return domain.Fail(domain.KindUnavailable, "notification_failed", "notification response unreadable or too large", "reason", "response_unreadable")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return domain.Fail(domain.KindUnavailable, "notification_failed", "notification service returned an error status", "reason", "http_status", "status", res.StatusCode)
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
		return domain.Fail(domain.KindUnavailable, "notification_failed", "notification response is malformed", "reason", "response_invalid")
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
	return domain.Fail(domain.KindUnavailable, "notification_failed", "notification service rejected the message", "reason", "rejected")
}
