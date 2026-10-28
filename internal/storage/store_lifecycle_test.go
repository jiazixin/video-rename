package storage

import (
	"bytes"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCloseReportsAllResourceFailures(t *testing.T) {
	t.Parallel()
	readFailure := errors.New("injected read pool close failure")
	writeFailure := errors.New("injected write pool close failure")
	openPool := func(failure error) *sql.DB {
		t.Helper()
		db := sql.OpenDB(fixtureConnector{closeErr: failure})
		if err := db.PingContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		return db
	}
	lock, err := os.CreateTemp(t.TempDir(), "instance-lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Store{read: openPool(readFailure), write: openPool(writeFailure), lock: lock}
	err = s.Close()
	for _, cause := range []error{readFailure, writeFailure, fs.ErrClosed} {
		if !errors.Is(err, cause) {
			t.Errorf("store close: got %v, want cause %v", err, cause)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("repeated close: got %v, want nil", err)
	}
}

func TestOpenFailureReleasesInstanceLockAndKeepsStoredData(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := s.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "INSERT INTO sessions VALUES('retained',123,'fingerprint')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(path + ".key")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	invalidKey := []byte("invalid fixture key")
	if err := os.WriteFile(path+".key", invalidKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if failed, err := Open(t.Context(), path); !errors.Is(err, errInvalidDatabaseKey) || failed != nil {
		t.Fatalf("invalid key open: got store=%v error=%v, want nil and invalid key", failed, err)
	}
	retained, err := os.ReadFile(path + ".key")
	if err != nil || !bytes.Equal(retained, invalidKey) {
		t.Fatalf("failed open changed key: got %d bytes error=%v", len(retained), err)
	}
	if err := os.WriteFile(path+".key", key, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	var count int
	if err := reopened.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM sessions WHERE token='retained'").Scan(&count)
	}); err != nil || count != 1 {
		t.Fatalf("data after failed open: got %d rows error=%v, want 1 and nil", count, err)
	}
}
