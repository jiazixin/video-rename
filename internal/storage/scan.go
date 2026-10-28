package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// ScanStore owns a disposable database and its private temporary directory.
// Callers must finish all View and Write callbacks before calling Close.
type ScanStore struct {
	write     *sql.DB
	read      *sql.DB
	path      string
	directory string
}

// NewScan creates an isolated stage from the caller's sole schema definition.
// The schema must be trusted application SQL, never external input.
func NewScan(ctx context.Context, schema string) (_ *ScanStore, err error) {
	directory, err := os.MkdirTemp("", "video-rename-scan-*")
	if err != nil {
		return nil, fmt.Errorf("create scan staging directory: %w", err)
	}
	s := &ScanStore{directory: directory, path: filepath.Join(directory, "scan.sqlite")}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create scan staging database: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("close new scan staging database: %w", err)
	}
	dsn := url.URL{Scheme: "file", Path: s.path}
	options := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(5000)"} {
		options.Add("_pragma", pragma)
	}
	options.Set("_txlock", "immediate")
	dsn.RawQuery = options.Encode()
	s.write, err = sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open scan staging writer: %w", err)
	}
	s.write.SetMaxOpenConns(1)
	if err = s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, schema)
		return err
	}); err != nil {
		return nil, fmt.Errorf("initialize scan staging schema: %w", err)
	}
	options.Del("_txlock")
	options.Del("_pragma")
	options.Set("mode", "ro")
	for _, pragma := range []string{"foreign_keys(1)", "query_only(1)", "busy_timeout(5000)"} {
		options.Add("_pragma", pragma)
	}
	dsn.RawQuery = options.Encode()
	s.read, err = sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open scan staging reader: %w", err)
	}
	s.read.SetMaxOpenConns(1)
	if err = s.read.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect scan staging reader: %w", err)
	}
	return s, nil
}

// Path identifies the stage for publication through Store.WriteScan.
func (s *ScanStore) Path() string {
	return s.path
}

// Write owns one immediate transaction, including rollback and commit.
// The callback must not retain tx or perform filesystem, network or process I/O.
func (s *ScanStore) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	return transaction(ctx, s.write, fn)
}

// View owns a read-only snapshot; all transactions and rows stay in the callback.
func (s *ScanStore) View(ctx context.Context, fn func(*sql.Tx) error) error {
	return transaction(ctx, s.read, fn)
}

// Close closes both pools and removes the stage with all SQLite sidecar files.
func (s *ScanStore) Close() error {
	var err error
	if s.read != nil {
		if closeErr := s.read.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close scan staging reader: %w", closeErr))
		}
	}
	if s.write != nil {
		if closeErr := s.write.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close scan staging writer: %w", closeErr))
		}
	}
	if removeErr := os.RemoveAll(s.directory); removeErr != nil {
		err = errors.Join(err, fmt.Errorf("remove scan staging directory: %w", removeErr))
	}
	return err
}
