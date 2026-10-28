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

	"golang.org/x/crypto/argon2"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/secret"
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
// sessions. mu protects attempts; loginGate bounds concurrent password hashes.
type Auth struct {
	db          *storage.Store
	username    string
	passwordKey []byte
	salt        []byte
	now         func() time.Time
	loginGate   chan struct{}
	fingerprint string
	mu          sync.Mutex
	attempts    map[string][]time.Time
}

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// New creates the authenticator for the administrator in cfg.
func New(ctx context.Context, db *storage.Store, cfg config.AuthConfig) (*Auth, error) {
	salt, err := db.AuthSalt(ctx)
	if err != nil {
		return nil, err
	}
	key := passwordKey(cfg.Password.Reveal(), []byte(salt))
	return &Auth{db: db, username: cfg.Username, salt: []byte(salt), passwordKey: key,
		fingerprint: hash(cfg.Username + "\x00" + hex.EncodeToString(key)), now: time.Now,
		loginGate: make(chan struct{}, 1), attempts: map[string][]time.Time{}}, nil
}

func passwordKey(password string, salt []byte) []byte {
	// Argon2id parameters are fixed and bounded; loginGate admits one hash at a time.
	return argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
}

var (
	// ErrUnauthenticated reports a missing or expired session.
	ErrUnauthenticated = domain.Fail(domain.KindUnauthenticated, domain.Record{Code: "unauthenticated", Message: "login required"})
	// ErrBadCredentials reports a username or password mismatch.
	ErrBadCredentials = domain.Fail(domain.KindUnauthenticated, domain.Record{Code: "invalid_credentials", Message: "invalid username or password"})
	// ErrRateLimited reports that the login failure budget is exhausted.
	ErrRateLimited = domain.Fail(domain.KindTooMany, domain.Record{Code: "rate_limited", Message: "too many login attempts"})
)

// pruneLocked drops failure records older than one minute; caller holds mu.
func (a *Auth) pruneLocked() {
	now := a.now()
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
		now := a.now()
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
	err = a.db.View(r.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(r.Context(), "SELECT expires,fingerprint FROM sessions WHERE token=?", hash(cookie.Value)).Scan(&expiry, &fingerprint)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthenticated
	}
	if err != nil {
		return err
	}
	if expiry <= a.now().Unix() || subtle.ConstantTimeCompare([]byte(fingerprint), []byte(a.fingerprint)) != 1 {
		return ErrUnauthenticated
	}
	if expiry-a.now().Unix() < int64(sessionRenewLeft/time.Second) {
		// Renewal is best effort for an already-valid session. A busy writer
		// must not hold up reads; a later request can retry the extension.
		renewCtx, cancel := context.WithTimeout(r.Context(), sessionRenewTimeout)
		_, err = storage.WriteValue(renewCtx, a.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(renewCtx, "UPDATE sessions SET expires=? WHERE token=? AND fingerprint=?", a.now().Add(sessionLifetime).Unix(), hash(cookie.Value), a.fingerprint)
		})
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
func (a *Auth) Username() string { return a.username }

// Credentials holds the administrator sign-in input.
type Credentials struct {
	Username string
	Password secret.Value
}

// Login checks the credentials, opens a session and sets its cookie on w.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, credentials Credentials) error {
	username := credentials.Username
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Non-TCP transports and synthetic requests have no host:port pair.
		// They share one bounded failure budget instead of bypassing it.
		ip = ""
	}
	select {
	case a.loginGate <- struct{}{}:
		defer func() { <-a.loginGate }()
	case <-r.Context().Done():
		return r.Context().Err()
	}
	if !a.allowLogin(ip, false) {
		return ErrRateLimited
	}
	user, wantUser := hash(username), hash(a.username)
	pass := passwordKey(credentials.Password.Reveal(), a.salt)
	valid := subtle.ConstantTimeCompare([]byte(user), []byte(wantUser))&subtle.ConstantTimeCompare(pass, a.passwordKey) == 1
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
	if err := a.db.Write(r.Context(), func(tx *sql.Tx) error {
		var err error
		if _, err = tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE expires<? OR fingerprint<>?", a.now().Unix(), a.fingerprint); err != nil {
			return err
		}
		if _, err = tx.ExecContext(r.Context(), "INSERT INTO sessions(token,expires,fingerprint) VALUES(?,?,?)", hash(token), a.now().Add(sessionLifetime).Unix(), a.fingerprint); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "vr_session", Value: token, Path: "/", MaxAge: int(sessionLifetime / time.Second), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	return nil
}

// Logout deletes the session of r and clears its cookie on w.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie("vr_session"); err == nil {
		if _, err = storage.WriteValue(r.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(r.Context(), "DELETE FROM sessions WHERE token=?", hash(cookie.Value))
		}); err != nil {
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
