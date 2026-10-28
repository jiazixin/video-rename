package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIdempotencyRetainsEmptyBody(t *testing.T) {
	t.Parallel()
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	request := IdempotencyRequest{Key: "empty-body", Fingerprint: "refresh", Now: time.Now()}
	if _, fresh, err := db.ReserveIdempotency(t.Context(), request); err != nil || !fresh {
		t.Fatalf("got fresh=%v, err=%v, want fresh reservation", fresh, err)
	}
	if err := db.CompleteIdempotency(t.Context(), IdempotencyResponse{Key: request.Key, Status: 202}); err != nil {
		t.Fatal(err)
	}
	got, fresh, err := db.ReserveIdempotency(t.Context(), request)
	if err != nil || fresh || got.Status != 202 || len(got.Body) != 0 {
		t.Fatalf("got response=%+v, fresh=%v, err=%v, want retained empty response", got, fresh, err)
	}
}

func TestIdempotencyRetainsResponsesAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.sqlite")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	request := IdempotencyRequest{Key: "retained-key", Fingerprint: "one", Now: now}
	if _, fresh, err := db.ReserveIdempotency(t.Context(), request); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	response := IdempotencyResponse{Key: request.Key, Status: 201, Header: `{"ETag":"\"v1\""}`, Body: []byte(`{"id":4}`)}
	if err := db.CompleteIdempotency(t.Context(), response); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	request.Now = now.Add(24*time.Hour - time.Millisecond)
	got, fresh, err := db.ReserveIdempotency(t.Context(), request)
	if err != nil || fresh || got.Status != 201 || got.Header != response.Header || string(got.Body) != string(response.Body) {
		t.Fatalf("response=%+v fresh=%v error=%v", got, fresh, err)
	}
	request.Fingerprint = "two"
	if _, _, err := db.ReserveIdempotency(t.Context(), request); !errors.Is(err, ErrIdempotencyReused) {
		t.Fatal(err)
	}
	request.Now = now.Add(24 * time.Hour)
	if _, fresh, err := db.ReserveIdempotency(t.Context(), request); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
}

func TestIdempotencyConcurrentReservationAndPendingRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.sqlite")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	request := IdempotencyRequest{Key: "concurrent-key", Fingerprint: "request", Now: time.Now()}
	var winners atomic.Int32
	var work sync.WaitGroup
	for range 16 {
		work.Go(func() {
			_, fresh, err := db.ReserveIdempotency(t.Context(), request)
			if err != nil && !errors.Is(err, ErrIdempotencyPending) {
				t.Error(err)
			}
			if fresh {
				winners.Add(1)
			}
		})
	}
	work.Wait()
	if winners.Load() != 1 {
		t.Fatalf("reservation winners=%d", winners.Load())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	request.Now = request.Now.Add(48 * time.Hour)
	if _, _, err := db.ReserveIdempotency(t.Context(), request); !errors.Is(err, ErrIdempotencyPending) {
		t.Fatalf("uncertain mutation became repeatable: %v", err)
	}
}

func TestIdempotencyCapacityRejectsBeforeWork(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "results.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<128)
  INSERT INTO request_results(key,fingerprint,status,headers,body,expires_ms) SELECT 'pending-'||x,'request',0,'',X'',? FROM n`, now.Add(time.Hour).UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := db.ReserveIdempotency(t.Context(), IdempotencyRequest{Key: "over-limit", Fingerprint: "request", Now: now}); fresh || !errors.Is(err, ErrIdempotencyCapacity) {
		t.Fatal(fresh, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := db.ReserveIdempotency(ctx, IdempotencyRequest{Key: "cancelled", Now: now}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
