package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func requestAuth(t *testing.T) (*Auth, http.Handler) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := New(db, config.AuthConfig{Username: "admin", Password: "test-password"})
	return a, testHandler(a, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
}

func TestAuthWritesHonorRequestCancellation(t *testing.T) {
	for _, action := range []string{"login", "logout"} {
		t.Run(action, func(t *testing.T) {
			a, handler := requestAuth(t)
			if _, err := a.DB.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("valid"), time.Now().Add(sessionLifetime).Unix(), a.fingerprint); err != nil {
				t.Fatal(err)
			}
			tx, err := a.DB.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := httptest.NewRequest("POST", "http://example.test/api/auth/"+action, strings.NewReader(`{"username":"admin","password":"test-password"}`)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "vr_session", Value: "valid"})
			before := a.DB.Stats().WaitCount
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { w := httptest.NewRecorder(); handler.ServeHTTP(w, r); done <- w }()
			deadline := time.Now().Add(time.Second)
			for a.DB.Stats().WaitCount == before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			cancel()
			select {
			case w := <-done:
				if w.Code < 300 || len(w.Result().Cookies()) != 0 {
					t.Fatal("cancelled write acknowledged", w.Code)
				}
			case <-time.After(time.Second):
				tx.Rollback()
				<-done
				t.Fatal("cancelled authentication request waited for writer")
			}
			tx.Rollback()
			var count int
			if err := a.DB.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 1 {
				t.Fatal("cancelled request mutated sessions", count, err)
			}
		})
	}
}

// Login body decoding is the app layer's; see internal/app/auth_test.go.
func TestAuthReadMethods(t *testing.T) {
	a, handler := requestAuth(t)
	if _, err := a.DB.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("valid"), time.Now().Add(sessionLifetime).Unix(), a.fingerprint); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, body, contentType, token string
		status                                       int
	}{
		{"authenticated HEAD", "HEAD", "/api/data", "", "", "valid", 204},
		{"anonymous HEAD", "HEAD", "/api/data", "", "", "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://example.test"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			if tc.token != "" {
				r.AddCookie(&http.Cookie{Name: "vr_session", Value: tc.token})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}

type gatedLoginBody struct {
	io.Reader
	ready *sync.WaitGroup
	gate  <-chan struct{}
	once  sync.Once
}

func (b *gatedLoginBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.ready.Done(); <-b.gate })
	return b.Reader.Read(p)
}

func TestConcurrentLoginFailuresRespectLimit(t *testing.T) {
	_, handler := requestAuth(t)
	const clients = 32
	var ready sync.WaitGroup
	ready.Add(clients)
	gate := make(chan struct{})
	results := make(chan int, clients)
	for range clients {
		go func() {
			body := &gatedLoginBody{Reader: strings.NewReader(`{"username":"admin","password":"wrong"}`), ready: &ready, gate: gate}
			r := httptest.NewRequest("POST", "http://example.test/api/auth/login", body)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			results <- w.Code
		}()
	}
	ready.Wait()
	close(gate)
	counts := map[int]int{}
	for range clients {
		counts[<-results]++
	}
	if counts[401] != 10 || counts[429] != clients-10 {
		t.Fatalf("concurrent limit bypass: %v", counts)
	}
}
