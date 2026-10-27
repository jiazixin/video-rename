// Package notifications delivers batch summaries independently of media processing.
package notifications

import (
	"encoding/json/v2"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// Kinds lists the supported notification channel kinds.
var Kinds = []string{"dingtalk", "wecom", "bark", "telegram", "webhook"}

// Credentials holds the secret settings of a channel.
type Credentials struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	Secret   string `json:"secret"`
	ChatID   string `json:"chat_id"`
}

// Channel is a notification channel as shown in settings, without credentials.
type Channel struct {
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	FailureOnly bool   `json:"failure_only"`
	HasEndpoint bool   `json:"has_endpoint"`
	HasToken    bool   `json:"has_token"`
	HasSecret   bool   `json:"has_secret"`
	ChatID      string `json:"chat_id"`
}

// Input is a change to a notification channel.
type Input struct {
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	FailureOnly bool   `json:"failure_only"`
	Credentials
	ClearSecret bool `json:"clear_secret"`
}

// Service stores channel settings and delivers batch summaries.
type Service struct {
	logger *slog.Logger
	DB     *storage.Store
	mu     sync.Mutex
	Send   func(Credentials, string, string, string) error
}

// New creates the notification service.
func New(db *storage.Store, logger *slog.Logger) *Service {
	s := &Service{logger: logger, DB: db}
	s.Send = sendMessage
	return s
}
func (s *Service) get(kind string) (Channel, Credentials, error) {
	c := Channel{Kind: kind}
	var encrypted string
	err := s.DB.QueryRow("SELECT enabled,failure_only,credentials FROM notification_channels WHERE kind=?", kind).Scan(&c.Enabled, &c.FailureOnly, &encrypted)
	if err != nil {
		return c, Credentials{}, err
	}
	raw, err := s.DB.Unseal(encrypted)
	if err != nil {
		return c, Credentials{}, domain.Fail(domain.KindInternal, "notification_credentials_unreadable", "notification credentials cannot be decrypted; check the database key")
	}
	var secret Credentials
	if err = json.Unmarshal([]byte(raw), &secret); err != nil {
		return c, secret, domain.Fail(domain.KindInternal, "notification_config_invalid", "stored notification configuration is invalid")
	}
	c.ChatID = secret.ChatID
	c.HasEndpoint = secret.Endpoint != ""
	c.HasToken = secret.Token != ""
	c.HasSecret = secret.Secret != ""
	return c, secret, nil
}

// List returns the notification channels.
func (s *Service) List() ([]Channel, error) {
	out := []Channel{}
	for _, kind := range Kinds {
		var count int
		if err := s.DB.QueryRow("SELECT count(*) FROM notification_channels WHERE kind=?", kind).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			out = append(out, Channel{Kind: kind})
			continue
		}
		c, _, err := s.get(kind)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
func validate(kind string, c Credentials) error {
	if !slices.Contains(Kinds, kind) {
		return errUnsupportedChannel
	}
	if kind == "telegram" {
		if !regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`).MatchString(c.Token) || strings.TrimSpace(c.ChatID) == "" {
			return domain.InvalidField("token", "telegram_incomplete")
		}
		return nil
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return domain.InvalidField("endpoint", "invalid_url")
	}
	if kind == "bark" && strings.Trim(u.Path, "/") == "" {
		return domain.InvalidField("endpoint", "bark_key_required")
	}
	if kind == "wecom" && (u.Scheme != "https" || u.Host != "qyapi.weixin.qq.com" || u.Path != "/cgi-bin/webhook/send" || u.Query().Get("key") == "") {
		return domain.InvalidField("endpoint", "wecom_webhook_required")
	}
	if kind == "dingtalk" && (u.Scheme != "https" || u.Host != "oapi.dingtalk.com" || u.Path != "/robot/send" || u.Query().Get("access_token") == "") {
		return domain.InvalidField("endpoint", "dingtalk_webhook_required")
	}
	return nil
}

// Save applies v to its channel.
func (s *Service) Save(v Input) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(Kinds, v.Kind) {
		return errUnsupportedChannel
	}
	var exists int
	if err := s.DB.QueryRow("SELECT count(*) FROM notification_channels WHERE kind=?", v.Kind).Scan(&exists); err != nil {
		return err
	}
	c := v.Credentials
	if exists > 0 {
		_, old, err := s.get(v.Kind)
		if err != nil {
			return err
		}
		if c.Endpoint == "" {
			c.Endpoint = old.Endpoint
		}
		if c.Token == "" {
			c.Token = old.Token
		}
		if c.Secret == "" && !v.ClearSecret {
			c.Secret = old.Secret
		}
	}
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Token = strings.TrimSpace(c.Token)
	c.ChatID = strings.TrimSpace(c.ChatID)
	if v.ClearSecret {
		c.Secret = ""
	}
	if v.Enabled || c.Endpoint != "" || c.Token != "" {
		if err := validate(v.Kind, c); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(c)
	encrypted, err := s.DB.Seal(string(raw))
	if err != nil {
		return err
	}
	// Saving a disabled channel cancels pending delivery. Re-enabling starts with new batches.
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO notification_channels(kind,enabled,failure_only,credentials,after_run) VALUES(?,?,?,?,(SELECT COALESCE(max(id),0) FROM runs)) ON CONFLICT(kind) DO UPDATE SET after_run=CASE WHEN notification_channels.enabled=0 THEN (SELECT COALESCE(max(id),0) FROM runs) ELSE notification_channels.after_run END,enabled=excluded.enabled,failure_only=excluded.failure_only,credentials=excluded.credentials`, v.Kind, v.Enabled, v.FailureOnly, encrypted)
	if err != nil {
		return err
	}
	if !v.Enabled {
		_, err = tx.Exec("UPDATE notification_deliveries SET status='cancelled' WHERE kind=? AND status IN ('pending','sending')", v.Kind)
		if err != nil {
			return err
		}
	}
	if v.FailureOnly {
		_, err = tx.Exec("UPDATE notification_deliveries SET status='cancelled' WHERE kind=? AND status='pending' AND run IN (SELECT id FROM runs WHERE failed=0 AND status IN ('completed','cancelled'))", v.Kind)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// errUnsupportedChannel rejects an unknown notification channel.
var errUnsupportedChannel = domain.Fail(domain.KindInvalid, "unsupported_channel", "unsupported notification channel")

// Test sends a test message through the channel of kind. The message is
// notification content, a recorded exception to the error-code rule.
func (s *Service) Test(kind string) error {
	_, c, err := s.get(kind)
	if err != nil {
		return domain.Fail(domain.KindConflict, "notification_not_configured", "save this channel first")
	}
	if err = validate(kind, c); err != nil {
		return err
	}
	return s.Send(c, kind, "video-rename · 测试通知", "通知连接正常。批次结束后，这里将显示处理数量、开始与完成时间等摘要。")
}
