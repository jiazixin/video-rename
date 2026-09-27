// Package storage owns the SQLite schema and connection lifecycle.
package storage

import (
	"crypto/cipher"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Store struct {
	*sql.DB
	read   *sql.DB
	lock   *os.File
	secret cipher.AEAD
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("数据库已被另一实例使用")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	dsn := url.URL{Scheme: "file", Path: abs}
	options := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "synchronous(FULL)", "busy_timeout(5000)"} {
		options.Add("_pragma", pragma)
	}
	dsn.RawQuery = options.Encode()
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, lock: lock}
	if err = s.initialize(); err != nil {
		s.Close()
		return nil, err
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		s.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		s.Close()
		return nil, err
	}
	if err = s.initSecret(path + ".key"); err != nil {
		s.Close()
		return nil, err
	}
	options.Set("mode", "ro")
	options.Add("_pragma", "query_only(1)")
	dsn.RawQuery = options.Encode()
	s.read, err = sql.Open("sqlite", dsn.String())
	if err == nil {
		s.read.SetMaxOpenConns(4)
		s.read.SetMaxIdleConns(4)
		err = s.read.Ping()
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Read supplies bounded, read-only WAL connections for queries and snapshots.
// Writes and read-modify-write transactions must continue to use DB.
func (s *Store) Read() *sql.DB { return s.read }

func (s *Store) Close() error {
	var readErr error
	if s.read != nil {
		readErr = s.read.Close()
	}
	err := s.DB.Close()
	if s.lock != nil {
		s.lock.Close()
	}
	return errors.Join(readErr, err)
}
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
