package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/storage"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProviderPayloadsAndBusinessErrors(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(kind, func(t *testing.T) {
			c := Credentials{Endpoint: "https://example.com/push?access_token=private", Token: "123:secret", Secret: "SEC-signature", ChatID: "-100123"}
			rejected := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("wrong request")
				}
				switch kind {
				case "telegram":
					if body["chat_id"] != c.ChatID || body["text"] != "title\n\nbody" {
						t.Error(body)
					}
				case "wecom", "dingtalk":
					if body["msgtype"] != "text" || body["text"].(map[string]any)["content"] != "title\n\nbody" {
						t.Error(body)
					}
				case "bark", "webhook":
					if body["title"] != "title" || body["body"] != "body" {
						t.Error(body)
					}
				}
				if kind == "dingtalk" {
					q := r.URL.Query()
					mac := hmac.New(sha256.New, []byte(c.Secret))
					mac.Write([]byte(q.Get("timestamp") + "\n" + c.Secret))
					if q.Get("sign") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
						t.Error("incorrect signature")
					}
				}
				if rejected {
					fmt.Fprint(w, `{"ok":false,"errcode":310000,"code":400,"message":"private"}`)
					return
				}
				fmt.Fprint(w, `{"ok":true,"errcode":0,"code":200}`)
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				r.URL.Scheme = target.Scheme
				r.URL.Host = target.Host
				return http.DefaultTransport.RoundTrip(r)
			})}
			if err := deliver(t.Context(), client, c, kind, "title", "body"); err != nil {
				t.Fatal(err)
			}
			rejected = true
			if err := deliver(t.Context(), client, c, kind, "title", "body"); kind != "webhook" && (err == nil || strings.Contains(err.Error(), "private")) {
				t.Fatal("bad error handling", err)
			}
		})
	}
}
func service(t *testing.T) *Service {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, slog.New(slog.DiscardHandler))
}
func addRun(t *testing.T, s *Service, status string, failed int) int64 {
	t.Helper()
	res, err := s.DB.Exec("INSERT INTO runs(source,targets,recursive,status,created,started,finished,discovered,done,failed,request_key) VALUES('manual','[1]',1,?,?,?, ?,3,2,?,?)", status, "2026-09-21T01:00:00Z", "2026-09-21T01:00:05Z", "2026-09-21T01:00:15Z", failed, fmt.Sprint(time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}
func TestQueueFilteringRetriesAndSecretPreservation(t *testing.T) {
	s := service(t)
	addRun(t, s, "completed", 0)
	if err := s.Save(t.Context(), Input{Kind: "webhook", Enabled: true, Endpoint: "http://127.0.0.1/push?key=private"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "private") {
		t.Fatal("secret exposed")
	}
	var encrypted string
	s.DB.QueryRow("SELECT credentials FROM notification_channels").Scan(&encrypted)
	if strings.Contains(encrypted, "private") {
		t.Fatal("secret stored as plaintext")
	}
	if err = s.Save(t.Context(), Input{Kind: "webhook", Enabled: true, FailureOnly: true}); err != nil {
		t.Fatal(err)
	}
	addRun(t, s, "completed", 0)
	id := addRun(t, s, "partial_failure", 1)
	calls := 0
	s.Send = func(_ context.Context, c Credentials, kind, title, body string) error {
		calls++
		if c.Endpoint != "http://127.0.0.1/push?key=private" {
			t.Error("blank edit lost secret")
		}
		if !strings.Contains(body, fmt.Sprintf("批次编号：#%d", id)) || !strings.Contains(body, "失败：1") || !strings.Contains(body, "10s") {
			t.Error(title, body)
		}
		return errors.New("mock offline")
	}
	for range 4 {
		s.DB.Exec("UPDATE notification_deliveries SET next_attempt='2000-01-01T00:00:00Z'")
		if err = s.tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	history, err := s.History(t.Context())
	if err != nil || len(history) != 1 || history[0].Run != id || history[0].Status != "failed" || calls != 3 {
		t.Fatal(history, calls, err)
	}
	var status string
	s.DB.QueryRow("SELECT status FROM runs WHERE id=?", id).Scan(&status)
	if status != "partial_failure" {
		t.Fatal("notification changed run")
	}
	if err = s.Save(t.Context(), Input{Kind: "webhook", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	addRun(t, s, "failed", 1)
	s.tick(t.Context())
	if calls != 3 {
		t.Fatal("disabled channel sent")
	}
}
func TestQueueSuccessIsDeduplicated(t *testing.T) {
	s := service(t)
	if err := s.Save(t.Context(), Input{Kind: "bark", Enabled: true, Endpoint: "http://127.0.0.1/device"}); err != nil {
		t.Fatal(err)
	}
	addRun(t, s, "completed", 0)
	calls := 0
	s.Send = func(context.Context, Credentials, string, string, string) error { calls++; return nil }
	for range 3 {
		if err := s.tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestNotificationRecoveryAttemptLimit(t *testing.T) {
	s := service(t)
	if err := s.Save(t.Context(), Input{Kind: "bark", Enabled: true, Endpoint: "http://127.0.0.1/device"}); err != nil {
		t.Fatal(err)
	}
	addRun(t, s, "completed", 0)
	if err := s.enqueue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE notification_deliveries SET status='sending',attempts=3"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		history, err := s.History(t.Context())
		if err == nil && history[0].Status == "failed" && history[0].Attempts == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(history, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBarkBatchBodyThroughJSONEndpoint(t *testing.T) {
	s := service(t)
	if _, err := s.DB.Exec("UPDATE notification_preferences SET timezone='Asia/Shanghai'"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bark/push" {
			t.Errorf("wrong route: %s", r.URL.Path)
		}
		var payload map[string]string
		if err := json.UnmarshalRead(r.Body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["device_key"] != "test-key" || payload["title"] != "video-rename · 批次失败（部分失败）" {
			t.Errorf("bad payload: %v", payload)
		}
		for _, want := range []string{"批次编号：#", "视频总数：3", "成功：2", "失败：1", "跳过：0", "开始时间：2026-09-21 09:00:05 +08:00 (Asia/Shanghai)", "完成时间：2026-09-21 09:00:15 +08:00 (Asia/Shanghai)", "处理耗时：10s"} {
			if !strings.Contains(payload["body"], want) {
				t.Errorf("missing %s: %s", want, payload["body"])
			}
		}
		fmt.Fprint(w, `{"code":200}`)
	}))
	defer server.Close()
	if err := s.Save(t.Context(), Input{Kind: "bark", Enabled: true, Endpoint: server.URL + "/bark/test-key/"}); err != nil {
		t.Fatal(err)
	}
	addRun(t, s, "partial_failure", 1)
	if err := s.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	history, err := s.History(t.Context())
	if err != nil || len(history) != 1 || history[0].Status != "sent" {
		t.Fatalf("delivery: %v %v", history, err)
	}
}

func TestNotificationTimezoneUsesDST(t *testing.T) {
	s := service(t)
	if err := s.Save(t.Context(), Input{Kind: "bark", Enabled: true, Endpoint: "http://127.0.0.1/device"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE notification_preferences SET timezone='America/New_York'"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ date, want string }{
		{"2026-01-21T01:00:00Z", "2026-01-20 20:00:00 -05:00 (America/New_York)"},
		{"2026-07-21T01:00:00Z", "2026-07-20 21:00:00 -04:00 (America/New_York)"},
	} {
		id := addRun(t, s, "completed", 0)
		if _, err := s.DB.Exec("UPDATE runs SET started=? WHERE id=?", tc.date, id); err != nil {
			t.Fatal(err)
		}
		if err := s.enqueue(t.Context()); err != nil {
			t.Fatal(err)
		}
		var body string
		if err := s.DB.QueryRow("SELECT body FROM notification_deliveries WHERE run=?", id).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, tc.want) {
			t.Fatal(body)
		}
	}
}
