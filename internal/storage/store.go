// Package storage owns the SQLite schema and connection lifecycle.
package storage

import (
	"context"
	"crypto/cipher"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

// Store is the SQLite database with a separate read pool.
type Store struct {
	*sql.DB
	read    *sql.DB
	lock    *os.File
	secret  cipher.AEAD
	reports reportCache
}

// Open opens or creates the database at path and applies its schema.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = abs
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// SQLite follows database symlinks. Resolve the same path before selecting
	// its instance lock and encryption key, so aliases cannot create two owners.
	if _, err := os.Lstat(path); err == nil {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		path = filepath.Join(parent, filepath.Base(path))
	} else {
		return nil, err
	}
	// Hard links keep separate lock, key and WAL paths for the same database
	// inode. SQLite cannot safely use that layout, even after symlink resolution.
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err == nil {
		if stat.Nlink > 1 {
			return nil, fmt.Errorf("database has multiple hard links and cannot be opened safely: %s", path)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("database is in use by another instance")
	}
	dsn := url.URL{Scheme: "file", Path: path}
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
	if err = s.initialize(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		s.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0o600); err != nil {
		s.Close()
		return nil, err
	}
	if err = s.initSecret(ctx, path+".key"); err != nil {
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
		err = s.read.PingContext(ctx)
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

// Close closes both connection pools.
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

// Now returns the current UTC time in RFC 3339 format with nanoseconds.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// recordTimeout bounds writes that record the outcome of work after its
// context has ended.
const recordTimeout = 30 * time.Second

// Detach returns a context for recording the outcome of work whose ctx may
// already be cancelled: a cancelled run, interrupted rename or failed delivery
// must still persist its result. It keeps ctx values and bounds the writes.
func Detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
}
