package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// keyedCall sends a keyed POST through the shared middleware and the
// idempotency layer in front of handler.
func keyedCall(ctx context.Context, a *App, handler http.HandlerFunc, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/test", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	a.middleware(a.idempotent(handler)).ServeHTTP(w, r)
	return w
}

func pendingReservations(t *testing.T, a *App) int {
	t.Helper()
	var count int
	if err := a.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM request_results WHERE status=0").Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIdempotencyDeliversOversizedResponseOnce(t *testing.T) {
	a, _ := markTestApp(t, 0)
	body := strings.Repeat("x", retainedResponseLimit+1)
	calls := 0
	handler := func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		// The handler does not learn whether its response was retained.
		_, _ = w.Write([]byte(body))
	}
	first := keyedCall(t.Context(), a, handler, "oversized-response")
	if first.Code != http.StatusCreated || first.Body.String() != body || first.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("got status %d with %d bytes, want 201 with the complete %d-byte response", first.Code, first.Body.Len(), len(body))
	}
	for range 2 {
		replay := keyedCall(t.Context(), a, handler, "oversized-response")
		if replay.Code != http.StatusConflict || !strings.Contains(replay.Body.String(), `"idempotency_result_unavailable"`) {
			t.Fatalf("got replay %d %s, want 409 idempotency_result_unavailable", replay.Code, replay.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("got %d handler calls, want 1", calls)
	}
	if got := pendingReservations(t, a); got != 0 {
		t.Fatalf("got %d pending reservations, want 0", got)
	}
}

func TestIdempotencyRecordsPanicAsFailure(t *testing.T) {
	a, _ := markTestApp(t, 0)
	calls := 0
	handler := func(http.ResponseWriter, *http.Request) {
		calls++
		panic("handler failed")
	}
	for range 2 {
		response := keyedCall(t.Context(), a, handler, "panicking-handler")
		if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"internal"`) {
			t.Fatalf("got %d %s, want 500 internal", response.Code, response.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("got %d handler calls, want 1", calls)
	}
	if got := pendingReservations(t, a); got != 0 {
		t.Fatalf("got %d pending reservations, want 0", got)
	}
}

// Clients retry a key only when no response arrived, so every response is
// final: a 503 is replayed like any other result instead of running again.
func TestIdempotencyReplaysFailures(t *testing.T) {
	a, _ := markTestApp(t, 0)
	t.Run("503", func(t *testing.T) {
		calls := 0
		handler := func(w http.ResponseWriter, r *http.Request) {
			calls++
			writeError(w, r, errShuttingDown)
		}
		for range 2 {
			response := keyedCall(t.Context(), a, handler, "unavailable-response")
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"shutting_down"`) {
				t.Fatalf("got %d %s, want 503 shutting_down", response.Code, response.Body.String())
			}
		}
		if calls != 1 {
			t.Fatalf("got %d handler calls, want 1", calls)
		}
	})
	// A handler stopped by its own request reported 500 internal, which the
	// client's retry after a timeout then showed as a server error.
	t.Run("cancelled during handler", func(t *testing.T) {
		calls := 0
		ctx, cancel := context.WithCancel(t.Context())
		handler := func(w http.ResponseWriter, r *http.Request) {
			calls++
			cancel()
			writeError(w, r, fmt.Errorf("read entries: %w", r.Context().Err()))
		}
		first := keyedCall(ctx, a, handler, "cancelled-during")
		retry := keyedCall(t.Context(), a, handler, "cancelled-during")
		for _, response := range []*httptest.ResponseRecorder{first, retry} {
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"request_cancelled"`) {
				t.Fatalf("got %d %s, want 503 request_cancelled", response.Code, response.Body.String())
			}
		}
		if calls != 1 {
			t.Fatalf("got %d handler calls, want 1", calls)
		}
	})
	t.Run("cancelled before handler", func(t *testing.T) {
		calls := 0
		handler := func(w http.ResponseWriter, _ *http.Request) {
			calls++
			send(w, http.StatusOK, map[string]int{"call": calls})
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if first := keyedCall(cancelled, a, handler, "cancelled-before"); first.Code != http.StatusServiceUnavailable || calls != 0 {
			t.Fatalf("got %d after %d handler calls, want 503 without running the handler", first.Code, calls)
		}
		// Nothing was reserved, so the key is still free.
		if retry := keyedCall(t.Context(), a, handler, "cancelled-before"); retry.Code != http.StatusOK {
			t.Fatalf("got retry %d %s, want 200", retry.Code, retry.Body.String())
		}
	})
	if got := pendingReservations(t, a); got != 0 {
		t.Fatalf("got %d pending reservations, want 0", got)
	}
}

func TestIdempotencyExpiredReservationsFreeCapacity(t *testing.T) {
	a, _ := markTestApp(t, 0)
	expired := time.Now().Add(-time.Minute).UnixMilli()
	// 128 reservations fill the whole retention capacity.
	if err := a.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<128)
INSERT INTO request_results(key,fingerprint,status,headers,body,expires_ms) SELECT 'interrupted-'||x,'request',0,'',X'',? FROM n`, expired)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	handler := func(w http.ResponseWriter, _ *http.Request) { send(w, http.StatusOK, map[string]bool{"ok": true}) }
	if response := keyedCall(t.Context(), a, handler, "after-expiry"); response.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200 once expired reservations are removed", response.Code, response.Body.String())
	}
	if got := pendingReservations(t, a); got != 0 {
		t.Fatalf("got %d pending reservations, want 0", got)
	}
}
