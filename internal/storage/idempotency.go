package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrIdempotencyReused rejects a key used with a different request.
	ErrIdempotencyReused = errors.New("idempotency key reused with a different request")
	// ErrIdempotencyPending prevents repeating work whose outcome is not recorded.
	ErrIdempotencyPending = errors.New("idempotent request is pending or requires outcome review")
	// ErrIdempotencyCapacity preserves the retention guarantee under pressure.
	ErrIdempotencyCapacity = errors.New("idempotency retention capacity reached")
)

const (
	idempotencyRetention  = 24 * time.Hour
	idempotencyMaxRecords = 10000
	idempotencyMaxBytes   = 64 << 20
	// IdempotencyResponseLimit bounds one retained response and its in-memory capture.
	IdempotencyResponseLimit = 512 << 10
)

// IdempotencyRequest identifies a request without retaining its secret body.
type IdempotencyRequest struct {
	Key         string
	Fingerprint string
	Now         time.Time
}

// IdempotencyResponse is the first completed response for a reserved key.
type IdempotencyResponse struct {
	Key    string
	Status int
	Header string
	Body   []byte
}

// ReserveIdempotency owns a fresh key, or returns its first completed response.
// Pending records survive restart: uncertainty must never repeat a file mutation.
func (s *Store) ReserveIdempotency(ctx context.Context, request IdempotencyRequest) (response IdempotencyResponse, fresh bool, err error) {
	err = s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM request_results WHERE expires_ms<=? AND status<>0", request.Now.UnixMilli()); err != nil {
			return err
		}
		var fingerprint string
		err := tx.QueryRowContext(ctx, "SELECT fingerprint,status,headers,body FROM request_results WHERE key=?", request.Key).Scan(&fingerprint, &response.Status, &response.Header, &response.Body)
		if err == nil {
			if fingerprint != request.Fingerprint {
				return ErrIdempotencyReused
			}
			if response.Status == 0 {
				return ErrIdempotencyPending
			}
			response.Key = request.Key
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count, size int64
		if err := tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(CASE WHEN status=0 THEN 524288 ELSE length(body)+length(headers) END),0) FROM request_results").Scan(&count, &size); err != nil {
			return err
		}
		if count >= idempotencyMaxRecords || size+IdempotencyResponseLimit > idempotencyMaxBytes {
			return ErrIdempotencyCapacity
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO request_results(key,fingerprint,status,headers,body,expires_ms) VALUES(?,?,0,'',X'',?)", request.Key, request.Fingerprint, request.Now.Add(idempotencyRetention).UnixMilli()); err != nil {
			return err
		}
		fresh = true
		return nil
	})
	if err != nil {
		err = fmt.Errorf("reserve request result: %w", err)
	}
	return
}

// CompleteIdempotency records a response before it is sent to the client.
func (s *Store) CompleteIdempotency(ctx context.Context, response IdempotencyResponse) error {
	if len(response.Body)+len(response.Header) > IdempotencyResponseLimit {
		return fmt.Errorf("complete request result: %w", ErrIdempotencyCapacity)
	}
	if response.Body == nil {
		// An empty HTTP body is an empty BLOB, not a SQL NULL.
		response.Body = []byte{}
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE request_results SET status=?,headers=?,body=? WHERE key=? AND status=0", response.Status, response.Header, response.Body, response.Key)
		if err != nil {
			return fmt.Errorf("complete request result: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("complete request result: reservation missing or already completed")
		}
		return nil
	})
}
