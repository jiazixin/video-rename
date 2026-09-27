package library

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type refreshTestEntry struct {
	os.DirEntry
	info func() (os.FileInfo, error)
}

func TestRefreshMetadataFailureKeepsCompleteGeneration(t *testing.T) {
	for _, cancelScan := range []bool{false, true} {
		name := "read error"
		if cancelScan {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			l, root, _ := fixture(t)
			put(t, filepath.Join(root, "old.mp4"))
			refresh(t, l, 1)
			var generation int64
			if err := l.DB.QueryRow("SELECT generation FROM directory_state WHERE dir=1").Scan(&generation); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
				put(t, filepath.Join(root, name))
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantErr := errors.New("metadata unavailable")
			if cancelScan {
				wantErr = context.Canceled
			}
			broken := refreshTestEntry{DirEntry: entries[2], info: func() (os.FileInfo, error) {
				if cancelScan {
					cancel()
					return entries[2].Info()
				}
				return nil, wantErr
			}}
			calls := 0
			l.mu.Lock()
			err = l.refreshDirectory(ctx, 1, func(n int) ([]os.DirEntry, error) {
				if n != 128 {
					t.Errorf("unbounded read: %d", n)
				}
				calls++
				if calls == 1 {
					return entries[:1], nil
				}
				return []os.DirEntry{entries[1], broken}, io.EOF
			})
			l.mu.Unlock()
			if !errors.Is(err, wantErr) {
				t.Fatalf("refresh error = %v, want %v", err, wantErr)
			}
			var published int64
			var scanError string
			if err := l.DB.QueryRow("SELECT generation,error FROM directory_state WHERE dir=1").Scan(&published, &scanError); err != nil {
				t.Fatal(err)
			}
			if published != generation || scanError == "" {
				t.Fatalf("failed scan published: generation %d -> %d, error %q", generation, published, scanError)
			}
			for name, want := range map[string]int{"old.mp4": 1, "a.mp4": 1, "b.mp4": 0, "c.mp4": 0} {
				var count int
				if err := l.DB.QueryRow("SELECT COUNT(*) FROM entries WHERE parent=1 AND name=? AND present=1", name).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != want {
					t.Errorf("%s present = %d, want %d", name, count, want)
				}
			}
			// A normal retry replaces the old generation and includes all entries.
			refresh(t, l, 1)
			for _, name := range []string{"old.mp4", "a.mp4", "b.mp4", "c.mp4"} {
				entry(t, l, 1, name)
			}
		})
	}
}

func (e refreshTestEntry) Info() (os.FileInfo, error) { return e.info() }

func TestRefreshSlowMetadataLeavesDatabaseAvailable(t *testing.T) {
	l, root, _ := fixture(t)
	put(t, filepath.Join(root, "a.mp4"))
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	slow := refreshTestEntry{DirEntry: entries[0], info: func() (os.FileInfo, error) {
		close(entered)
		<-release
		return entries[0].Info()
	}}
	go func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		done <- l.refreshDirectory(t.Context(), 1, func(int) ([]os.DirEntry, error) {
			return []os.DirEntry{slow}, io.EOF
		})
	}()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Errorf("refresh: %v", err)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach metadata read")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var count int
	if err := l.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM entries").Scan(&count); err != nil {
		t.Fatalf("database read blocked by filesystem metadata: %v", err)
	}
	if _, err := l.DB.ExecContext(ctx, "UPDATE entries SET observed=observed WHERE id=1"); err != nil {
		t.Fatalf("database write blocked by filesystem metadata: %v", err)
	}
}
