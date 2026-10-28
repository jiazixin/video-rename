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
	"sync"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"

	"video-rename/internal/timefmt"
)

// Store is the SQLite database with a separate read pool. closeMu serializes
// shutdown and protects the instance lock's lifetime.
type Store struct {
	write   *sql.DB
	read    *sql.DB
	lock    *os.File
	secret  cipher.AEAD
	reports reportCache
	closeMu sync.Mutex
}

// Open opens or creates the database at path and applies its schema.
func Open(ctx context.Context, path string) (_ *Store, err error) {
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
	s := &Store{lock: lock}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("database is in use by another instance: %w", err)
		}
		return nil, fmt.Errorf("lock database: %w", err)
	}
	dsn := url.URL{Scheme: "file", Path: path}
	options := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(5000)"} {
		options.Add("_pragma", pragma)
	}
	options.Set("_txlock", "immediate")
	if err := inspectSchema(ctx, dsn); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err = errors.Join(file.Chmod(0o600), file.Close()); err != nil {
		return nil, err
	}
	dsn.RawQuery = options.Encode()
	s.write, err = sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	s.write.SetMaxOpenConns(1)
	if err = s.initialize(ctx); err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	if err = s.initSecret(ctx, path+".key"); err != nil {
		return nil, err
	}
	options.Del("_txlock")
	options.Del("_pragma")
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)"} {
		options.Add("_pragma", pragma)
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
		return nil, err
	}
	return s, nil
}

// Close closes both connection pools and releases the instance lock. Repeated
// calls are safe, including after a partially completed Open.
func (s *Store) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	var err error
	if s.read != nil {
		if closeErr := s.read.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database reader: %w", closeErr))
		}
	}
	if s.write != nil {
		if closeErr := s.write.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database writer: %w", closeErr))
		}
	}
	if s.lock != nil {
		if closeErr := s.lock.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database instance lock: %w", closeErr))
		}
		s.lock = nil
	}
	return err
}

// Now returns the current UTC time with fixed millisecond precision.
func Now() string { return timefmt.Format(time.Now()) }

// recordTimeout bounds writes that record the outcome of work after its
// context has ended.
const recordTimeout = 30 * time.Second

// Detach returns a context for recording the outcome of work whose ctx may
// already be cancelled: a cancelled run, interrupted rename or failed delivery
// must still persist its result. It keeps ctx values and bounds the writes.
func Detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
}
