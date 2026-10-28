package framecache

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeaseFailureUsesConfiguredLogger(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	c, err := New(t.Context(), Options{Base: t.TempDir(), LimitBytes: 1 << 20, Logger: slog.New(slog.NewJSONHandler(&output, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	lease, err := c.Acquire(t.Context(), strings.Repeat("a", 64), 100000, build)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate loss of the managed directory before recording lease release.
	if err := os.RemoveAll(filepath.Join(c.root, lease.key)); err != nil {
		t.Fatal(err)
	}
	lease.Release(false)
	if !strings.Contains(output.String(), `"msg":"frame_cache_update_failed"`) {
		t.Fatalf("got %s, want the configured logger to report persistence failure", output.String())
	}
}
