package app

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// requestKey also supplies a private journal key for an unkeyed, non-retryable
// request. The response cache is used only when the caller supplies the header.
func requestKey(r *http.Request) string {
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		return key
	}
	return rand.Text()
}

type capturedResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (c *capturedResponse) Header() http.Header { return c.header }
func (c *capturedResponse) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}
func (c *capturedResponse) Write(data []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.body.Len()+len(data) > storage.IdempotencyResponseLimit-4096 {
		c.overflow = true
		return 0, errors.New("response retention limit exceeded")
	}
	return c.body.Write(data)
}

func (a *App) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if r.Method != http.MethodPost || key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !idempotencyKeyPattern.MatchString(key) {
			writeError(w, r, domain.InvalidField("Idempotency-Key", "invalid"))
			return
		}
		if r.Body == nil {
			r.Body = http.NoBody
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			if _, large := errors.AsType[*http.MaxBytesError](err); large {
				err = errTooLarge
			}
			writeError(w, r, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		digest := sha256.Sum256(append([]byte(r.Method+"\n"+r.URL.RequestURI()+"\n"+r.Header.Get("If-Match")+"\n"), raw...))
		// Serialize identical keys only. No database transaction spans handler I/O.
		release, err := a.lockRequest(r, key)
		if err != nil {
			writeError(w, r, idempotencyError(err))
			return
		}
		defer release()
		response, fresh, err := a.db.ReserveIdempotency(r.Context(), storage.IdempotencyRequest{Key: key, Fingerprint: hex.EncodeToString(digest[:]), Now: time.Now()})
		if err != nil {
			writeError(w, r, idempotencyError(err))
			return
		}
		if !fresh {
			replayResponse(w, response)
			return
		}
		captured := &capturedResponse{header: make(http.Header)}
		// Retain the API's JSON 404/405 response, including Allow, rather than
		// re-encoding the router's plain-text response on every replay.
		next.ServeHTTP(&routerErrorWriter{responseRecorder: &responseRecorder{ResponseWriter: captured}, request: r}, r)
		if captured.overflow {
			writeError(w, r, errors.New("request response exceeds retention limit; inspect pending result"))
			return
		}
		if captured.status == 0 {
			captured.status = http.StatusOK
		}
		headers := map[string]string{}
		for _, name := range []string{"Content-Type", "ETag", "Location", "Retry-After", "Allow"} {
			if value := captured.header.Get(name); value != "" {
				headers[name] = value
			}
		}
		encoded, err := json.Marshal(headers)
		if err != nil {
			writeError(w, r, err)
			return
		}
		response = storage.IdempotencyResponse{Key: key, Status: captured.status, Header: string(encoded), Body: captured.body.Bytes()}
		record, cancel := storage.Detach(r.Context())
		defer cancel()
		if err := a.db.CompleteIdempotency(record, response); err != nil {
			writeError(w, r, err)
			return
		}
		replayResponse(w, response)
	})
}

func (a *App) lockRequest(r *http.Request, key string) (func(), error) {
	for {
		a.requestMu.Lock()
		if a.requests == nil {
			a.requests = map[string]chan struct{}{}
		}
		done, exists := a.requests[key]
		if !exists {
			if len(a.requests) >= 256 {
				a.requestMu.Unlock()
				return nil, storage.ErrIdempotencyCapacity
			}
			done = make(chan struct{})
			a.requests[key] = done
			a.requestMu.Unlock()
			return func() { a.requestMu.Lock(); delete(a.requests, key); close(done); a.requestMu.Unlock() }, nil
		}
		a.requestMu.Unlock()
		select {
		case <-done:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
}

func replayResponse(w http.ResponseWriter, response storage.IdempotencyResponse) {
	var headers map[string]string
	if err := json.Unmarshal([]byte(response.Header), &headers); err != nil {
		panic(err)
	}
	for key, value := range headers {
		w.Header().Set(key, value)
	}
	w.WriteHeader(response.Status)
	// The result is durable. A disconnected client can replay the same key.
	_, _ = w.Write(response.Body)
}
func idempotencyError(err error) error {
	switch {
	case errors.Is(err, storage.ErrIdempotencyReused):
		return domain.Fail(domain.KindConflict, domain.Record{Code: "idempotency_key_reused", Message: "idempotency key belongs to a different request"})
	case errors.Is(err, storage.ErrIdempotencyPending):
		return domain.Fail(domain.KindConflict, domain.Record{Code: "idempotency_pending", Message: "request outcome is pending; inspect the resource before submitting new work"})
	case errors.Is(err, storage.ErrIdempotencyCapacity):
		return domain.Fail(domain.KindConflict, domain.Record{Code: "idempotency_capacity", Message: "request result retention capacity is full"})
	default:
		return err
	}
}
