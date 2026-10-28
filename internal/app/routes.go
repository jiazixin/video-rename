package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"video-rename/internal/auth"
	"video-rename/internal/domain"
	"video-rename/internal/secret"
)

// Handler returns the HTTP handler for the API and the embedded web assets.
func (a *App) Handler() http.Handler {
	api := &router{mux: http.NewServeMux()}
	a.modelRoutes(api)
	a.promptRoutes(api)
	a.notificationRoutes(api)
	a.samplingRoutes(api)
	a.systemSettingsRoutes(api)
	a.libraryScanRoutes(api)
	a.numberingRoutes(api)
	api.HandleFunc("GET /api/logs/{id}/diagnostics", a.logDiagnostics)
	api.HandleFunc("GET /api/extractions", a.extractions)
	api.HandleFunc("GET /api/extractions/{id}", a.extractionDetail)
	api.HandleFunc("POST /api/extractions/clear", a.clearExtractions)
	api.HandleFunc("POST /api/logs/clear", a.clearHistory)
	api.HandleFunc("POST /api/logs/retry", a.retryHistory)
	api.HandleFunc("GET /api/entries/{id}/video", a.playback)
	api.HandleFunc("GET /api/entries/{id}/audio", a.audioPlayback)
	api.HandleFunc("GET /api/entries/{id}/image", a.imagePreview)
	api.HandleFunc("GET /api/entries/{id}/image-neighbor", a.adjacentImage)
	api.HandleFunc("GET /api/entries/status", a.entryStatus)
	api.HandleFunc("POST /api/entries/processed", a.markEntries)
	api.HandleFunc("GET /api/entries/processed/status/{key}", a.markStatus)
	a.libraryRoutes(api)
	a.runRoutes(api)
	a.scheduleRoutes(api)
	api.HandleFunc("GET /api/logs", a.logs)
	api.HandleFunc("GET /api/logs/batches", a.logBatches)
	api.HandleFunc("GET /api/usage/analytics", a.usageAnalytics)
	api.HandleFunc("GET /api/runs/analytics", a.runsAnalytics)
	api.HandleFunc("GET /api/auth/session", a.session)
	api.HandleFunc("POST /api/auth/logout", a.logout)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		send(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/info", a.info)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.Handle("/api/", a.requireSession(api.mux))
	mux.Handle("/", assetHandler())
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, errOriginRejected)
	}))
	return a.middleware(protection.Handler(mux))
}

// router registers API routes; a numeric {id} that does not parse is a 400
// invalid_argument before the handler runs. Numbering batches use text IDs.
type router struct{ mux *http.ServeMux }

func (rt *router) HandleFunc(pattern string, handler http.HandlerFunc) {
	if !strings.Contains(pattern, "{id}") || strings.Contains(pattern, "/api/numberings/") {
		rt.mux.HandleFunc(pattern, handler)
		return
	}
	rt.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err != nil || id <= 0 {
			writeError(w, r, domain.InvalidField("id", "invalid_number"))
			return
		}
		handler(w, r)
	})
}

// numericQuery names the integer query parameters of the API; a value that
// does not parse is reported instead of silently becoming 0.
var numericQuery = regexp.MustCompile(`^(limit|offset|run|[a-z]+_offset)$`)

// queryFields keeps each list endpoint's contract explicit. Repeated filters
// are ambiguous, and unsupported fields must not select a different protocol.
func queryFields(query url.Values, allowed ...string) error {
	for name, values := range query {
		if !slices.Contains(allowed, name) || len(values) != 1 {
			return domain.InvalidField(name, "invalid")
		}
		if len(values[0]) > 2000 {
			return domain.InvalidField(name, "too_long")
		}
		if name == "offset" || strings.HasSuffix(name, "_offset") {
			offset, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || offset < 0 {
				return domain.InvalidField(name, "out_of_range")
			}
		}
		if name == "limit" {
			limit, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || limit <= 0 || limit > 200 {
				return domain.InvalidField(name, "out_of_range")
			}
			// Analytics endpoints expose a bounded variable row count; tables offer three sizes.
			if !slices.Contains(allowed, "timezone") && limit != 50 && limit != 100 && limit != 200 {
				return domain.InvalidField(name, "invalid_choice")
			}
		}
	}
	return nil
}

func malformedNumber(query url.Values) (string, bool) {
	for name, values := range query {
		if !numericQuery.MatchString(name) {
			continue
		}
		for _, value := range values {
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				return name, true
			}
		}
	}
	return "", false
}

// requireSession answers API requests without a valid session with 401.
func (a *App) requireSession(api *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Auth.Authenticate(w, r); err != nil {
			writeError(w, r, err)
			return
		}
		if name, ok := malformedNumber(r.URL.Query()); ok {
			writeError(w, r, domain.InvalidField(name, "invalid_number"))
			return
		}
		a.idempotent(api).ServeHTTP(w, r)
	})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string       `json:"username"`
		Password secret.Value `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := a.Auth.Login(w, r, auth.Credentials{Username: input.Username, Password: input.Password}); err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, map[string]string{"username": a.Auth.Username()})
}

func (a *App) session(w http.ResponseWriter, _ *http.Request) {
	send(w, http.StatusOK, map[string]string{"username": a.Auth.Username()})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.Logout(w, r); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// info reports the service version and the optional features clients may use.
func (a *App) info(w http.ResponseWriter, r *http.Request) {
	send(w, http.StatusOK, struct {
		Build
		Capabilities []string `json:"capabilities"`
	}{Build: a.build, Capabilities: []string{"image_numbering", "notifications", "schedules"}})
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

type loggerKey struct{}

// requestLogger returns the logger carrying the request ID.
func requestLogger(r *http.Request) *slog.Logger {
	if logger, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}

// contentSecurityPolicy applies to the web pages and the API alike. Vue sets
// bound styles through the CSSOM, so style-src needs no 'unsafe-inline'.
const contentSecurityPolicy = "default-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'"

// middleware wraps next in the shared request handling, outermost first:
// request ID, access log, panic recovery, security headers and the JSON form
// of the router's own 404 / 405 responses on /api/ paths.
func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
			requestID = rand.Text()
		}
		w.Header().Set("X-Request-ID", requestID)
		logger := a.logger.With("request_id", requestID)
		r = r.WithContext(context.WithValue(r.Context(), loggerKey{}, logger))
		recorder := &responseRecorder{ResponseWriter: w}
		started := time.Now()
		defer recorder.logAccess(logger, r, started)
		defer recoverPanic(recorder, r, logger)
		header := recorder.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "same-origin")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/healthz" {
			next.ServeHTTP(recorder, r)
			return
		}
		header.Set("Cache-Control", "no-store")
		next.ServeHTTP(&routerErrorWriter{responseRecorder: recorder, request: r}, r)
	})
}

// recoverPanic logs a handler panic and answers 500 internal unless the
// response has started. http.ErrAbortHandler keeps aborting the response.
func recoverPanic(w *responseRecorder, r *http.Request, logger *slog.Logger) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if recovered == http.ErrAbortHandler {
		panic(recovered)
	}
	logger.Error("http_handler_panic", "method", r.Method, "path", r.URL.Path, "error", fmt.Sprint(recovered))
	if !w.wroteHeader {
		writeError(w, r, fmt.Errorf("panic: %v", recovered))
	}
}

// logAccess records API requests; pages, assets and health checks log at debug.
func (w *responseRecorder) logAccess(logger *slog.Logger, r *http.Request, started time.Time) {
	level := slog.LevelInfo
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		level = slog.LevelDebug
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	logger.Log(r.Context(), level, "http_request",
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"duration_ms", time.Since(started).Milliseconds(),
		"bytes", w.bytes,
	)
}

// routerErrorWriter replaces the router's plain-text 404 and 405 responses
// with the JSON error body. Handlers write JSON errors themselves.
type routerErrorWriter struct {
	*responseRecorder
	request  *http.Request
	replaced bool
}

func (w *routerErrorWriter) WriteHeader(status int) {
	plainText := strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain")
	if !w.wroteHeader && plainText && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) {
		w.replaced = true
		w.Header().Del("Content-Length")
		if status == http.StatusNotFound {
			writeError(w.responseRecorder, w.request, errNotFound)
		} else {
			writeError(w.responseRecorder, w.request, errMethodNotAllowed)
		}
		return
	}
	w.responseRecorder.WriteHeader(status)
}

func (w *routerErrorWriter) Write(payload []byte) (int, error) {
	if w.replaced {
		return len(payload), nil
	}
	return w.responseRecorder.Write(payload)
}

// responseRecorder keeps the optional interfaces used by media playback while
// collecting the final status and response size.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(payload []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	count, err := r.ResponseWriter.Write(payload)
	r.bytes += int64(count)
	return count, err
}

func (r *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	count, err := io.Copy(r.ResponseWriter, reader)
	r.bytes += count
	return count, err
}

func (r *responseRecorder) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	// Flush cannot return an error through http.Flusher; the server owns a disconnected stream.
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
