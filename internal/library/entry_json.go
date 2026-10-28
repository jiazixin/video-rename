package library

import (
	"encoding/json/v2"
	"time"

	"video-rename/internal/timefmt"
)

// MarshalJSON exposes a millisecond timestamp while keeping the nanosecond
// file fingerprint private. Safety checks always use ModifiedNS, not this view.
func (e Entry) MarshalJSON() ([]byte, error) {
	type entry Entry
	return json.Marshal(struct {
		entry
		Mtime timefmt.Time `json:"mtime"`
	}{entry: entry(e), Mtime: timefmt.Time(time.Unix(0, e.ModifiedNS))})
}
