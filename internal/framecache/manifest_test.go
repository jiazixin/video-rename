package framecache

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/timefmt"
)

func TestManifestUsesMillisecondsAndCanonicalTime(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 28, 12, 13, 14, 123456789, time.FixedZone("fixture", 8*60*60))
	r := record{Key: strings.Repeat("a", 64), LastUsed: timefmt.Time(at)}
	r.Set.Plan = domain.FramePlan{Duration: 5*time.Second + 678*time.Millisecond, Times: []time.Duration{1234 * time.Millisecond}}
	r.Set.Frames = []domain.Frame{{Planned: 1234 * time.Millisecond, Actual: 1235 * time.Millisecond, Path: "frame-00.jpg"}}
	raw, err := marshalRecord(&r)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		LastUsed string `json:"last_used"`
		Plan     struct {
			DurationMS int64 `json:"duration_ms"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.LastUsed != "2026-10-28T04:13:14.123Z" || stored.Plan.DurationMS != 5678 {
		t.Fatalf("got stored=%+v, want UTC milliseconds and duration 5678ms", stored)
	}
	got, err := unmarshalRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Set.Plan.Duration != r.Set.Plan.Duration || got.Set.Frames[0].Actual != 1235*time.Millisecond {
		t.Fatalf("got plan=%+v frames=%+v, want millisecond round trip", got.Set.Plan, got.Set.Frames)
	}
	for _, raw := range []string{
		`{"version":0,"last_used":"2026-10-28T04:13:14.123Z"}`,
		`{"version":1,"last_used":"2026-10-28T04:13:14Z"}`,
		`{"version":1,"last_used":"2026-10-28T04:13:14.123Z","plan":{"duration_ms":9223372036854775807}}`,
		`{"version":1,"last_used":"2026-10-28T04:13:14.123Z","plan":{"duration":1000000000}}`,
	} {
		if _, err := unmarshalRecord([]byte(raw)); err == nil {
			t.Fatalf("got no error for invalid manifest %s, want rejection", raw)
		}
	}
}

func TestCacheReadsBoundSizeAndRejectEscapes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := readCacheFile(t.Context(), dir, "manifest.json"); err == nil {
		t.Fatal("got no error for escaping symlink, want rejection")
	}
	if err := os.WriteFile(filepath.Join(dir, "large.json"), make([]byte, cacheFileLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCacheFile(t.Context(), dir, "large.json"); err == nil {
		t.Fatal("got no error for oversized manifest, want rejection")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readCacheFile(ctx, dir, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation before filesystem access", err)
	}
}
