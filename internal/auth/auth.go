// Package auth provides a single configured administrator and revocable sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
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

// Auth authenticates the configured administrator and manages revocable
// sessions.
type Auth struct {
	DB          *storage.Store
	Config      config.AuthConfig
	fingerprint string
	mu          sync.Mutex
	attempts    map[string][]time.Time
}

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// New creates the authenticator for the administrator in cfg.
func New(db *storage.Store, cfg config.AuthConfig) *Auth {
	return &Auth{DB: db, Config: cfg, fingerprint: hash(cfg.Username + "\x00" + cfg.Password), attempts: map[string][]time.Time{}}
}

// Failures reported to the HTTP layer.
var (
	ErrUnauthenticated = domain.Fail(domain.KindUnauthenticated, "unauthenticated", "login required")
	ErrBadCredentials  = domain.Fail(domain.KindUnauthenticated, "invalid_credentials", "invalid username or password")
	ErrRateLimited     = domain.Fail(domain.KindTooMany, "rate_limited", "too many login attempts")
)

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

// allowLogin checks the budget and records a failure under the same lock.
// Successful sign-ins do not consume the failure budget.
func (a *Auth) allowLogin(ip string, failed bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked()
	if len(a.attempts[ip]) >= 10 || len(a.attempts["*"]) >= 100 {
		return false
	}
	if failed {
		now := time.Now()
		a.attempts[ip] = append(a.attempts[ip], now)
		a.attempts["*"] = append(a.attempts["*"], now)
	}
	return true
}

// Authenticate checks the session cookie of r and slides its expiry forward,
// re-issuing the cookie on w. It returns ErrUnauthenticated without a valid
// session.
func (a *Auth) Authenticate(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie("vr_session")
	if err != nil {
		return ErrUnauthenticated
	}
	var expiry int64
	var fingerprint string
	err = a.DB.Read().QueryRowContext(r.Context(), "SELECT expires,fingerprint FROM sessions WHERE token=?", hash(cookie.Value)).Scan(&expiry, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthenticated
	}
	if err != nil {
		return err
	}
	if expiry <= time.Now().Unix() || fingerprint != a.fingerprint {
		return ErrUnauthenticated
	}
	if expiry-time.Now().Unix() < int64(sessionRenewLeft/time.Second) {
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
	return nil
}

// Username returns the configured administrator name.
func (a *Auth) Username() string { return a.Config.Username }

// Login checks the credentials, opens a session and sets its cookie on w.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, username, password string) error {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	user, pass := hash(username), hash(password)
	wantUser, wantPass := hash(a.Config.Username), hash(a.Config.Password)
	valid := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser))&subtle.ConstantTimeCompare([]byte(pass), []byte(wantPass)) == 1
	if !a.allowLogin(ip, !valid) {
		return ErrRateLimited
	}
	if !valid {
		return ErrBadCredentials
	}
	token := rand.Text()
	secure := secureCookie(r)
	if secure {
		token = secureSessionPrefix + token
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE expires<? OR fingerprint<>?", time.Now().Unix(), a.fingerprint); err != nil {
		return err
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO sessions(token,expires,fingerprint) VALUES(?,?,?)", hash(token), time.Now().Add(sessionLifetime).Unix(), a.fingerprint); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "vr_session", Value: token, Path: "/", MaxAge: int(sessionLifetime / time.Second), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	return nil
}

// Logout deletes the session of r and clears its cookie on w.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie("vr_session"); err == nil {
		if _, err = a.DB.ExecContext(r.Context(), "DELETE FROM sessions WHERE token=?", hash(cookie.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "vr_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureCookie(r)})
	return nil
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || httpsOrigin(r)
}

// httpsOrigin reports whether the browser sent an https Origin for this host,
// as it does behind a TLS-terminating reverse proxy. X-Forwarded-* headers from
// arbitrary clients are not trusted.
func httpsOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && u.Scheme == "https" && u.Host == r.Host
}
