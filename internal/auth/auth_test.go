package auth

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/secret"
	"video-rename/internal/storage"
)

func TestSessionValidationWhileWriterBusy(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	a := newAuth(t, db, config.AuthConfig{Username: "admin", Password: "test-password"})
	freshExpiry := time.Now().Add(sessionLifetime).Unix()
	for _, session := range []struct {
		token, fingerprint string
		expiry             int64
	}{
		{"valid", a.fingerprint, freshExpiry},
		{"expired", a.fingerprint, time.Now().Add(-time.Hour).Unix()},
		{"changed-password", "other-fingerprint", freshExpiry},
	} {
		if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), "INSERT INTO sessions VALUES(?,?,?)", hash(session.token), session.expiry, session.fingerprint)
		}); err != nil {
			t.Fatal(err)
		}
	}
	finish := holdAuthWrite(t, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "DELETE FROM sessions WHERE token=?", hash("valid"))
		return err
	})
	handler := testHandler(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		name, path, method, token, fetchSite string
		status                               int
	}{
		{"session", "/api/auth/session", "GET", "valid", "", 200},
		{"authenticated request", "/api/data", "GET", "valid", "", 204},
		{"authenticated mutation", "/api/data", "POST", "valid", "same-origin", 204},
		{"cross-site mutation", "/api/data", "POST", "valid", "cross-site", 403},
		{"unknown token", "/api/data", "GET", "unknown", "", 401},
		{"expired token", "/api/data", "GET", "expired", "", 401},
		{"changed password", "/api/data", "GET", "changed-password", "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://example.test"+tc.path, strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			if tc.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			request.AddCookie(&http.Cookie{Name: "vr_session", Value: tc.token})
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				done <- response
			}()
			select {
			case response := <-done:
				if response.Code != tc.status {
					t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tc.status)
				}
				if cookies := response.Result().Cookies(); len(cookies) != 0 {
					t.Fatal("fresh session unexpectedly renewed", cookies)
				}
				if tc.path == "/api/auth/session" {
					var session map[string]string
					if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session["username"] != "admin" {
						t.Fatal("session did not use committed credentials", session, err)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("session validation waited for the writer")
			}
		})
		if t.Failed() {
			return
		}
	}
	finish(true)
	request := httptest.NewRequest("GET", "http://example.test/api/data", nil)
	request.AddCookie(&http.Cookie{Name: "vr_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("committed session revocation was not observed", response.Code)
	}
}

func TestSessionValidationReadWaitHonorsCancellation(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	a := newAuth(t, db, config.AuthConfig{Username: "admin", Password: "test-password"})
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "INSERT INTO sessions VALUES(?,?,?)", hash("valid"), time.Now().Add(sessionLifetime).Unix(), a.fingerprint)
	}); err != nil {
		t.Fatal(err)
	}
	holdAuthReads(t, db)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequest("GET", "http://example.test/api/auth/session", nil).WithContext(ctx)
	request.AddCookie(&http.Cookie{Name: "vr_session", Value: "valid"})
	handler := testHandler(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	done := make(chan *httptest.ResponseRecorder, 1)
	before := db.PoolStats().Reads.WaitCount
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		done <- response
	}()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	wait := time.NewTimer(time.Second)
	defer wait.Stop()
	for db.PoolStats().Reads.WaitCount == before {
		select {
		case response := <-done:
			t.Fatalf("request returned before the read pool was available: %d", response.Code)
		case <-poll.C:
		case <-wait.C:
			t.Fatal("session query did not wait on the exhausted read pool")
		}
	}
	cancel()
	select {
	case response := <-done:
		if response.Code != 500 || !strings.Contains(response.Body.String(), `"code":"internal"`) {
			t.Fatalf("cancelled query response=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("session query did not stop after request cancellation")
	}
}

func TestSessionRenewalWhileWriterBusy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
	}{
		{"bounded wait", false},
		{"request cancellation", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			a := newAuth(t, db, config.AuthConfig{Username: "admin", Password: "test-password"})
			token := secureSessionPrefix + "valid"
			expiry := time.Now().Add(time.Hour).Unix()
			if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(t.Context(), "INSERT INTO sessions VALUES(?,?,?)", hash(token), expiry, a.fingerprint)
			}); err != nil {
				t.Fatal(err)
			}
			finish := holdAuthWrite(t, db, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), "UPDATE sessions SET expires=0 WHERE token=?", hash(token))
				return err
			})
			handler := testHandler(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			request := func(ctx context.Context) *http.Request {
				r := httptest.NewRequest("GET", "http://example.test/api/auth/session", nil).WithContext(ctx)
				r.AddCookie(&http.Cookie{Name: "vr_session", Value: token})
				return r
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan *httptest.ResponseRecorder, 1)
			pending := true
			defer func() {
				cancel()
				finish(false)
				if pending {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("renewal request did not exit after writer rollback")
					}
				}
			}()
			before := db.PoolStats().Writes.WaitCount
			go func() {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, request(ctx))
				done <- w
			}()
			budget := time.Second
			if tc.cancel {
				// Cancel only after authentication has succeeded and renewal is
				// waiting for the sole writer, rather than canceling the auth read.
				deadline := time.NewTimer(time.Second)
				defer deadline.Stop()
				poll := time.Tick(time.Millisecond)
				for db.PoolStats().Writes.WaitCount == before {
					select {
					case w := <-done:
						pending = false
						t.Fatalf("request returned before waiting to renew: %d", w.Code)
					case <-poll:
					case <-deadline.C:
						t.Fatal("renewal did not wait on the busy writer")
					}
				}
				cancel()
				budget = 50 * time.Millisecond
			}
			var response *httptest.ResponseRecorder
			select {
			case response = <-done:
				pending = false
			case <-time.After(budget):
				t.Fatal("valid session waited for the busy writer instead of finishing renewal")
			}
			if response.Code != http.StatusOK || len(response.Result().Cookies()) != 0 {
				t.Fatalf("busy renewal changed authentication or issued a cookie: status=%d cookies=%v", response.Code, response.Result().Cookies())
			}
			var session map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session["username"] != "admin" {
				t.Fatalf("busy renewal did not preserve committed session: %+v err=%v", session, err)
			}
			var storedExpiry int64
			if err := db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT expires FROM sessions WHERE token=?", hash(token)).Scan(&storedExpiry)
			}); err != nil || storedExpiry != expiry {
				t.Fatalf("failed renewal changed expiry: got=%d want=%d err=%v", storedExpiry, expiry, err)
			}
			finish(false)
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request(t.Context()))
			cookies := response.Result().Cookies()
			if response.Code != http.StatusOK || len(cookies) != 1 || cookies[0].Value != token || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != int(sessionLifetime/time.Second) {
				t.Fatalf("retry did not renew the secure session: status=%d cookies=%v", response.Code, cookies)
			}
			if err := db.View(t.Context(), func(tx *sql.Tx) error {
				return tx.QueryRowContext(t.Context(), "SELECT expires FROM sessions WHERE token=?", hash(token)).Scan(&storedExpiry)
			}); err != nil || storedExpiry < time.Now().Add(11*time.Hour).Unix() {
				t.Fatalf("retry did not extend expiry: got=%d err=%v", storedExpiry, err)
			}
		})
	}
}

func TestSessionCrossOriginAndLogout(t *testing.T) {
	db, e := storage.Open(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	a := newAuth(t, db, config.AuthConfig{Username: "admin", Password: "test-password-123"})
	h := testHandler(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	call := func(path, method, body string, c *http.Cookie, fetchSite, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if c != nil {
			r.AddCookie(c)
		}
		if fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("/api/data", "GET", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call("/api/auth/login", "POST", `{"username":"admin","password":"test-password-123"}`, nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("missing protected cookie")
	}
	if w = call("/api/data", "POST", "{}", cookies[0], "cross-site", ""); w.Code != 403 {
		t.Fatal("cross-site write allowed")
	}
	if w = call("/api/data", "POST", "{}", cookies[0], "", "https://evil.test"); w.Code != 403 {
		t.Fatal("cross origin allowed")
	}
	if w = call("/api/data", "POST", "{}", cookies[0], "same-origin", "http://example.test"); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w = call("/api/auth/logout", "POST", "{}", cookies[0], "", ""); w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
	if w = call("/api/data", "GET", "", cookies[0], "", ""); w.Code != 401 {
		t.Fatal("revoked cookie accepted")
	}
}

func TestSessionSlidingRenewal(t *testing.T) {
	db, e := storage.Open(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	a := newAuth(t, db, config.AuthConfig{Username: "admin", Password: "test-password-123"})
	h := testHandler(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	login := func() *http.Cookie {
		r := httptest.NewRequest("POST", "http://example.test/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password-123"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("login: %d", w.Code)
		}
		return w.Result().Cookies()[0]
	}
	get := func(c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://example.test/api/data", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	expires := func() int64 {
		var expiry int64
		if e := db.View(t.Context(), func(tx *sql.Tx) error {
			return tx.QueryRowContext(t.Context(), "SELECT max(expires) FROM sessions").Scan(&expiry)
		}); e != nil {
			t.Fatal(e)
		}
		return expiry
	}
	cookie := login()
	// Fresh session keeps most of its lifetime: no renewal, no re-issued cookie.
	fresh := expires()
	if w := get(cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if expires() != fresh {
		t.Fatal("fresh session should not renew")
	}
	for _, c := range get(cookie).Result().Cookies() {
		if c.Name == "vr_session" {
			t.Fatal("fresh session should not re-issue cookie")
		}
	}
	// Past half-life the next request slides expiry forward and refreshes the cookie.
	if _, e := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE sessions SET expires=?", time.Now().Add(time.Hour).Unix())
	}); e != nil {
		t.Fatal(e)
	}
	w := get(cookie)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	if renewed := expires(); renewed < time.Now().Add(11*time.Hour).Unix() {
		t.Fatalf("session not renewed: %d", renewed)
	}
	reissued := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "vr_session" {
			reissued = true
			if c.MaxAge != int(12*time.Hour/time.Second) || !c.HttpOnly {
				t.Fatalf("unexpected renewed cookie: %+v", c)
			}
		}
	}
	if !reissued {
		t.Fatal("renewal must re-issue cookie")
	}
	// Transient storage failures must not masquerade as logged-out (401).
	if err := db.Close(); err != nil {
		t.Error(err)
	}
	if w := get(cookie); w.Code != 500 {
		t.Fatalf("storage failure should be 500, got %d", w.Code)
	}
}

func TestLoginRateLimitCountsFailuresOnly(t *testing.T) {
	db, e := storage.Open(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	a := newAuth(t, db, config.AuthConfig{Username: "admin", Password: "test-password-123"})
	h := testHandler(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	login := func(user, pass string) int {
		r := httptest.NewRequest("POST", "http://example.test/api/auth/login", strings.NewReader(`{"username":"`+user+`","password":"`+pass+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// successful logins do not count toward the rate limit: a valid user may log in repeatedly.
	for i := range 12 {
		if code := login("admin", "test-password-123"); code != 200 {
			t.Fatalf("successful login #%d was rate limited: %d", i+1, code)
		}
	}
	// only consecutive failures count: 10 failures trigger the limit.
	for i := range 10 {
		if code := login("admin", "wrong-password"); code != 401 {
			t.Fatalf("failed login #%d returned %d, want 401", i+1, code)
		}
	}
	if code := login("admin", "test-password-123"); code != 429 {
		t.Fatalf("failure limit returned %d, want 429", code)
	}
}

func TestCookieSecuritySurvivesProxyRenewal(t *testing.T) {
	for _, tc := range []struct {
		name, url, origin string
		secure            bool
	}{
		{"TLS", "https://example.test", "", true},
		{"HTTPS proxy", "http://example.test", "https://example.test", true},
		{"HTTP LAN ignores forwarded header", "http://example.test", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			a := newAuth(t, db, config.AuthConfig{Username: "admin", Password: "test-password"})
			handler := testHandler(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			login := httptest.NewRequest("POST", tc.url+"/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
			login.Header.Set("Content-Type", "application/json")
			login.Header.Set("Origin", tc.origin)
			login.Header.Set("X-Forwarded-Proto", "https")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, login)
			cookies := response.Result().Cookies()
			if response.Code != 200 || len(cookies) != 1 || cookies[0].Secure != tc.secure {
				t.Fatalf("login=%d cookies=%v", response.Code, cookies)
			}
			var session map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(t.Context(), "UPDATE sessions SET expires=?", time.Now().Add(time.Hour).Unix())
			}); err != nil {
				t.Fatal(err)
			}
			// The TLS proxy forwards this GET as HTTP, without an Origin header.
			renew := httptest.NewRequest("GET", "http://example.test/api/auth/session", nil)
			renew.AddCookie(cookies[0])
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, renew)
			refreshed := response.Result().Cookies()
			if response.Code != 200 || len(refreshed) != 1 || refreshed[0].Secure != tc.secure || refreshed[0].Value != cookies[0].Value {
				t.Fatalf("renewal=%d cookies=%v", response.Code, refreshed)
			}
			var renewed map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &renewed); err != nil || renewed["username"] != "admin" {
				t.Fatal("renewal changed the session", renewed, err)
			}
			post := httptest.NewRequest("POST", "http://example.test/api/data", strings.NewReader("{}"))
			post.AddCookie(refreshed[0])
			post.Header.Set("Content-Type", "application/json")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, post)
			if response.Code != 204 {
				t.Fatal("authenticated mutation failed after renewal", response.Code)
			}
			post.Header.Set("Origin", "https://evil.test")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, post)
			if response.Code != 403 {
				t.Fatal("forged origin bypassed the cross-origin check", response.Code)
			}
			forged := *cookies[0]
			forged.Value = secureSessionPrefix + forged.Value
			renew = httptest.NewRequest("GET", "http://example.test/api/auth/session", nil)
			renew.AddCookie(&forged)
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, renew)
			if response.Code != 401 {
				t.Fatal("forged token prefix authenticated", response.Code)
			}
		})
	}
}

// testHandler composes the session checks the way the app's router does:
// cross-site writes are rejected, /api/auth/login, session and logout are
// answered here, and other requests reach next only with a valid session.
func testHandler(t *testing.T, a *Auth, next http.Handler) http.Handler {
	t.Helper()
	writeJSON := func(w http.ResponseWriter, value any) {
		t.Helper()
		if err := json.MarshalWrite(w, value); err != nil {
			t.Error(err)
		}
	}
	writeErr := func(w http.ResponseWriter, err error) {
		status, code := http.StatusInternalServerError, "internal"
		if e, ok := domain.AsError(err); ok {
			code = e.Code
			switch e.Kind {
			case domain.KindUnauthenticated:
				status = http.StatusUnauthorized
			case domain.KindTooMany:
				status = http.StatusTooManyRequests
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		writeJSON(w, map[string]any{"error": map[string]string{"code": code}})
	}
	protection := http.NewCrossOriginProtection()
	return protection.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			var input struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := json.UnmarshalRead(r.Body, &input); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if err := a.Login(w, r, Credentials{Username: input.Username, Password: secret.Value(input.Password)}); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, map[string]string{"username": a.Username()})
			return
		}
		if err := a.Authenticate(w, r); err != nil {
			writeErr(w, err)
			return
		}
		switch r.URL.Path {
		case "/api/auth/session":
			writeJSON(w, map[string]string{"username": a.Username()})
		case "/api/auth/logout":
			if err := a.Logout(w, r); err != nil {
				writeErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			next.ServeHTTP(w, r)
		}
	}))
}

func newAuth(t *testing.T, db *storage.Store, cfg config.AuthConfig) *Auth {
	t.Helper()
	a, err := New(t.Context(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func holdAuthWrite(t *testing.T, db *storage.Store, fn func(*sql.Tx) error) func(bool) {
	t.Helper()
	entered := make(chan error, 1)
	release := make(chan bool, 1)
	done := make(chan error, 1)
	rollback := errors.New("test rollback")
	go func() {
		done <- db.Write(t.Context(), func(tx *sql.Tx) error {
			err := fn(tx)
			entered <- err
			if err != nil {
				return err
			}
			if <-release {
				return nil
			}
			return rollback
		})
	}()
	if err := <-entered; err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	finish := func(commit bool) {
		once.Do(func() {
			release <- commit
			if err := <-done; err != nil && !errors.Is(err, rollback) {
				t.Error(err)
			}
		})
	}
	t.Cleanup(func() { finish(false) })
	return finish
}

func holdAuthReads(t *testing.T, db *storage.Store) {
	t.Helper()
	count := db.PoolStats().Reads.MaxOpenConnections
	entered := make(chan struct{}, count)
	release := make(chan struct{})
	done := make(chan error, count)
	for range count {
		go func() {
			done <- db.View(t.Context(), func(*sql.Tx) error { entered <- struct{}{}; <-release; return nil })
		}()
	}
	for range count {
		<-entered
	}
	t.Cleanup(func() {
		close(release)
		for range count {
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		}
	})
}
