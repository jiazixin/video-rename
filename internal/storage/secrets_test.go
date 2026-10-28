package storage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadSecretKeyRequiresExactly32Bytes(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 31, 32, 33, 1 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "fixture.key")
			want := bytes.Repeat([]byte{0xa5}, size)
			if err := os.WriteFile(path, want, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readSecretKey(path)
			if size == databaseKeySize {
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("valid key: got %d bytes error=%v, want %d bytes and nil", len(got), err, size)
				}
			} else if !errors.Is(err, errInvalidDatabaseKey) || got != nil {
				t.Fatalf("invalid key: got %d bytes error=%v, want nil and invalid key", len(got), err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, want) {
				t.Fatalf("key validation changed file: got %d bytes error=%v", len(after), err)
			}
		})
	}
}

func TestReadSecretKeyRejectsSpecialFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	fifo := filepath.Join(directory, "fixture.key")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, fifo} {
		if key, err := readSecretKey(path); !errors.Is(err, errInvalidDatabaseKey) || key != nil {
			t.Fatalf("special key file: got %d bytes error=%v, want nil and invalid key", len(key), err)
		}
	}
}
