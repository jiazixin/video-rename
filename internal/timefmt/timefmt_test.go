package timefmt

import (
	"encoding/json/v2"
	"testing"
	"time"
)

func TestCanonicalTimestamp(t *testing.T) {
	t.Parallel()
	instant := time.Date(2026, 10, 27, 12, 0, 0, 123456789, time.FixedZone("fixture", 8*60*60))
	got, err := json.Marshal(Time(instant))
	if err != nil || string(got) != `"2026-10-27T04:00:00.123Z"` {
		t.Fatalf("got %s, %v, want UTC milliseconds", got, err)
	}
	var value Time
	if err := json.Unmarshal(got, &value); err != nil || !time.Time(value).Equal(instant.Truncate(time.Millisecond)) {
		t.Fatalf("got %v, %v, want millisecond round trip", value, err)
	}
	for _, invalid := range []string{"2026-10-27T04:00:00Z", "2026-10-27T04:00:00.1234Z", "2026-10-27T12:00:00.123+08:00"} {
		if err := value.UnmarshalText([]byte(invalid)); err == nil {
			t.Fatalf("got accepted %q, want canonical timestamp only", invalid)
		}
	}
}
