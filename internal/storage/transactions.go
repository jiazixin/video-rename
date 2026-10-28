package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Write owns one immediate write transaction. The callback must not retain tx
// or perform network, subprocess or filesystem I/O while holding it, except the
// final file identity checks documented in README.md's code exceptions.
func (s *Store) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return transaction(ctx, s.write, fn)
}

// View owns a read-only snapshot. The callback must finish and close all rows
// before returning; neither the transaction nor its rows may escape it.
func (s *Store) View(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return transaction(ctx, s.read, fn)
}

// ReadValue returns a value built entirely within one read-only snapshot.
func ReadValue[T any](ctx context.Context, s *Store, fn func(*sql.Tx) (T, error)) (T, error) {
	var value T
	err := s.View(ctx, func(tx *sql.Tx) error {
		var err error
		value, err = fn(tx)
		return err
	})
	return value, err
}

// WriteValue returns a value only after the write transaction commits.
func WriteValue[T any](ctx context.Context, s *Store, fn func(*sql.Tx) (T, error)) (T, error) {
	var value T
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var err error
		value, err = fn(tx)
		return err
	})
	return value, err
}

func transaction(ctx context.Context, db txBeginner, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin database transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("roll back database transaction: %w", rollbackErr))
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit database transaction: %w", err)
	}
	return nil
}

type txBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// WriteScan publishes an isolated scan database in the sole writer transaction.
// The attachment is scoped to this call and cannot leak into later operations.
func (s *Store) WriteScan(ctx context.Context, path string, fn func(*sql.Tx) error) (err error) {
	conn, err := s.write.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if _, err = conn.ExecContext(ctx, "ATTACH DATABASE ? AS directory_scan", path); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := Detach(ctx)
		defer cancel()
		_, detachErr := conn.ExecContext(cleanup, "DETACH DATABASE directory_scan")
		err = errors.Join(err, detachErr)
	}()
	return transaction(ctx, conn, fn)
}

// PoolMetrics reports connection-pool pressure without exposing either pool.
type PoolMetrics struct{ Reads, Writes sql.DBStats }

// PoolStats returns a snapshot of the reader and writer connection pools.
func (s *Store) PoolStats() PoolMetrics {
	return PoolMetrics{Reads: s.read.Stats(), Writes: s.write.Stats()}
}
