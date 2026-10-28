package library

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"
)

func TestEntryTimestampSeparatesDisplayFromFingerprint(t *testing.T) {
	t.Parallel()
	stamp := time.Date(2026, 10, 27, 15, 0, 0, 123456789, time.FixedZone("fixture", 8*60*60))
	entry := Entry{ID: 1, ModifiedNS: stamp.UnixNano()}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Mtime string `json:"mtime"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Mtime != "2026-10-27T07:00:00.123Z" || strings.Contains(string(raw), "mtime_ns") {
		t.Fatalf("got %s, want UTC millisecond display without internal fingerprint", raw)
	}
	if entry.ModifiedNS != stamp.UnixNano() || entry.ModifiedNS == stamp.Truncate(time.Millisecond).UnixNano() {
		t.Fatalf("got %d, want original nanosecond file evidence", entry.ModifiedNS)
	}
}
