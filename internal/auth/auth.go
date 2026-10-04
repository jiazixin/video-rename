// Package auth provides a single configured administrator and revocable sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

// Session expiry slides forward while the session is used: a fixed 12-hour
// cap would force an active user back to the login page exactly 12 hours
// after sign-in regardless of activity. Renewal runs at most once per
// renewal margin and must re-issue the cookie, or the browser would drop
// the session at the original MaxAge even though SQLite still accepts it.
const (
	sessionLifetime     = 12 * time.Hour
	sessionRenewLeft    = sessionLifetime / 2
	sessionRenewTimeout = 100 * time.Millisecond
	secureSessionPrefix = "secure."
)

type Auth struct {
	DB          *storage.Store
	Config      config.AuthConfig
	fingerprint string
	mu          sync.Mutex
	attempts    map[string][]time.Time
}

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func New(db *storage.Store, cfg config.AuthConfig) *Auth {
	return &Auth{DB: db, Config: cfg, fingerprint: hash(cfg.Username + "\x00" + cfg.Password), attempts: map[string][]time.Time{}}
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"message": msg})
}

// pruneLocked drops failure records older than one minute; caller holds mu.
func (a *Auth) pruneLocked() {
	now := time.Now()
	for k, list := range a.attempts {
		fresh := list[:0]
		for _, t := range list {
			if now.Sub(t) < time.Minute {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) == 0 {
			delete(a.attempts, k)
		} else {
			a.attempts[k] = fresh
		}
	}
}

// limited reports whether ip has too many recent failed logins.
// Only failures count: successful sign-ins must not lock a legitimate
// user out of repeated logins.
func (a *Auth) limited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked()
	return len(a.attempts[ip]) >= 10 || len(a.attempts["*"]) >= 100
}

// failed records one failed login attempt for rate limiting.
func (a *Auth) failed(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts[ip] = append(a.attempts[ip], time.Now())
	a.attempts["*"] = append(a.attempts["*"], time.Now())
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// A TLS-terminating reverse proxy may pass a validated HTTPS Origin with the
	// original Host. Do not trust X-Forwarded-* from arbitrary clients.
	return u.Host == r.Host && (u.Scheme == "https" || (u.Scheme == "http" && r.TLS == nil))
}
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method != "GET" && (!sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")) {
			fail(w, 403, "请求来源或格式无效")
			return
		}
		if r.URL.Path == "/api/auth/login" && r.Method == "POST" {
			a.login(w, r)
			return
		}
		cookie, err := r.Cookie("vr_session")
		var csrf string
		var expiry int64
		var fingerprint string
		if err == nil {
			err = a.DB.Read().QueryRowContext(r.Context(), "SELECT csrf,expires,fingerprint FROM sessions WHERE token=?", hash(cookie.Value)).Scan(&csrf, &expiry, &fingerprint)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				fail(w, 500, "服务暂不可用")
				return
			}
		}
		if err != nil || expiry <= time.Now().Unix() || fingerprint != a.fingerprint {
			fail(w, 401, "请先登录")
			return
		}
		if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
			fail(w, 403, "请求已失效，请刷新页面")
			return
		}
		if r.URL.Path != "/api/auth/logout" && expiry-time.Now().Unix() < int64(sessionRenewLeft/time.Second) {
			// Renewal is best effort for an already-valid session. A busy writer
			// must not hold up reads; a later request can retry the extension.
			renewCtx, cancel := context.WithTimeout(r.Context(), sessionRenewTimeout)
			_, err = a.DB.ExecContext(renewCtx, "UPDATE sessions SET expires=? WHERE token=? AND fingerprint=?", time.Now().Add(sessionLifetime).Unix(), hash(cookie.Value), a.fingerprint)
			cancel()
			if err == nil {
				// The token's hash authenticates the original cookie policy, so a
				// proxied GET without Origin cannot downgrade an HTTPS session.
				secure := secureCookie(r) || strings.HasPrefix(cookie.Value, secureSessionPrefix)
				http.SetCookie(w, &http.Cookie{Name: "vr_session", Value: cookie.Value, Path: "/", MaxAge: int(sessionLifetime / time.Second), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
			}
		}
		if r.URL.Path == "/api/auth/session" && r.Method == "GET" {
			write(w, 200, map[string]string{"username": a.Config.Username, "csrf": csrf})
			return
		}
		if r.URL.Path == "/api/auth/logout" && r.Method == "POST" {
			if _, err = a.DB.Exec("DELETE FROM sessions WHERE token=?", hash(cookie.Value)); err != nil {
				fail(w, 500, "退出失败")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "vr_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureCookie(r)})
			write(w, 200, map[string]bool{"ok": true})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a.limited(ip) {
		fail(w, 429, "登录尝试过于频繁，请稍后重试")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.UnmarshalRead(r.Body, &input); err != nil {
		fail(w, 400, "登录格式无效")
		return
	}
	user, pass := hash(input.Username), hash(input.Password)
	wantUser, wantPass := hash(a.Config.Username), hash(a.Config.Password)
	if subtle.ConstantTimeCompare([]byte(user), []byte(wantUser))&subtle.ConstantTimeCompare([]byte(pass), []byte(wantPass)) != 1 {
		a.failed(ip)
		fail(w, 401, "账号或密码错误")
		return
	}
	token, csrf := Token(), Token()
	secure := secureCookie(r)
	if secure {
		token = secureSessionPrefix + token
	}
	tx, err := a.DB.Begin()
	if err != nil {
		fail(w, 503, "登录暂不可用")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("DELETE FROM sessions WHERE expires<? OR fingerprint<>?", time.Now().Unix(), a.fingerprint)
	if err == nil {
		_, err = tx.Exec("INSERT INTO sessions VALUES(?,?,?,?)", hash(token), csrf, time.Now().Add(sessionLifetime).Unix(), a.fingerprint)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "登录暂不可用")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "vr_session", Value: token, Path: "/", MaxAge: int(sessionLifetime / time.Second), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	write(w, 200, map[string]string{"username": a.Config.Username, "csrf": csrf})
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || (sameOrigin(r) && strings.HasPrefix(r.Header.Get("Origin"), "https://"))
}
