package app

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
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

// retainedResponseLimit leaves room for the selected headers within one record.
const retainedResponseLimit = storage.IdempotencyResponseLimit - 4096

// errResultNotRetained replays a request whose first response was delivered
// but was too large to keep.
var errResultNotRetained = &httpFailure{http.StatusConflict, errorBody{Code: "idempotency_result_unavailable", Message: "request completed; its response was too large to retain, inspect the resource"}}

// errHandlerFailed is the recorded outcome of a handler that panicked.
var errHandlerFailed = &httpFailure{http.StatusInternalServerError, errorBody{Code: "internal", Message: "internal error"}}

// capturedResponse buffers a keyed response until it is durable. A response
// over the retention limit cannot be replayed: overflow records that outcome
// first, then the buffered and remaining bytes stream to the client.
type capturedResponse struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	client    http.ResponseWriter
	overflow  func() error
	streaming bool
	err       error
}

func (c *capturedResponse) Header() http.Header { return c.header }

func (c *capturedResponse) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *capturedResponse) Write(data []byte) (int, error) {
	if c.streaming {
		return c.client.Write(data)
	}
	if c.err != nil {
		return 0, c.err
	}
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.body.Len()+len(data) <= retainedResponseLimit {
		return c.body.Write(data)
	}
	if c.err = c.overflow(); c.err != nil {
		return 0, c.err
	}
	c.streaming = true
	maps.Copy(c.client.Header(), c.header)
	c.client.WriteHeader(c.status)
	if _, err := c.client.Write(c.body.Bytes()); err != nil {
		return 0, err
	}
	c.body.Reset()
	return c.client.Write(data)
}

// failureResponse is the retained form of a transport failure.
func failureResponse(key string, failure *httpFailure) (storage.IdempotencyResponse, error) {
	body, err := json.Marshal(map[string]any{"error": failure.body})
	if err != nil {
		return storage.IdempotencyResponse{}, err
	}
	return storage.IdempotencyResponse{Key: key, Status: failure.status, Header: `{"Content-Type":"application/json; charset=utf-8"}`, Body: body}, nil
}

// recordFailure completes the reservation of key with a fixed failure. The
// request context may have ended; the outcome must still be recorded.
func (a *App) recordFailure(r *http.Request, key string, failure *httpFailure) error {
	response, err := failureResponse(key, failure)
	if err != nil {
		return err
	}
	record, cancel := storage.Detach(r.Context())
	defer cancel()
	return a.db.CompleteIdempotency(record, response)
}

// idempotent replays the first recorded response of a keyed POST, whatever its
// status: clients retry a key only when no response arrived. Every path out of
// the handler settles the reservation, including an oversized response and a
// panic. Only a process stopped in between leaves a reservation, which rejects
// the key until it expires.
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
		captured := &capturedResponse{header: make(http.Header), client: w, overflow: func() error {
			return a.recordFailure(r, key, errResultNotRetained)
		}}
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			// An aborted response has no outcome to record. Any other panic is a
			// final failure unless the response is already recorded and streaming.
			if recovered != http.ErrAbortHandler && !captured.streaming {
				if err := a.recordFailure(r, key, errHandlerFailed); err != nil {
					requestLogger(r).Error("request_result_save_failed", "error", err)
				}
			}
			panic(recovered)
		}()
		// Retain the API's JSON 404/405 response, including Allow, rather than
		// re-encoding the router's plain-text response on every replay.
		next.ServeHTTP(&routerErrorWriter{responseRecorder: &responseRecorder{ResponseWriter: captured}, request: r}, r)
		if captured.err != nil {
			writeError(w, r, fmt.Errorf("record oversized response: %w", captured.err))
			return
		}
		if captured.streaming {
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
		return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonIdempotencyKeyReused, Message: "idempotency key belongs to a different request"})
	case errors.Is(err, storage.ErrIdempotencyPending):
		return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonIdempotencyPending, Message: "request outcome is pending; inspect the resource before submitting new work"})
	case errors.Is(err, storage.ErrIdempotencyCapacity):
		return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonIdempotencyCapacity, Message: "request result retention capacity is full"})
	default:
		return err
	}
}
