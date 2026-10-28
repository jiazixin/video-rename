// Package timefmt defines the UTC millisecond timestamp used by persistence and APIs.
package timefmt

import (
	"fmt"
	"time"
)

// Layout is the canonical timestamp layout, including exactly three fraction digits.
const Layout = "2006-01-02T15:04:05.000Z"

// Time serializes an instant as a UTC millisecond timestamp.
type Time time.Time

// IsZero reports whether the timestamp is absent.
func (t Time) IsZero() bool { return time.Time(t).IsZero() }

// Compare orders two instants independently of their locations.
func (t Time) Compare(other Time) int { return time.Time(t).Compare(time.Time(other)) }

// MarshalText formats the instant independently of its original location.
func (t Time) MarshalText() ([]byte, error) {
	return []byte(time.Time(t).UTC().Format(Layout)), nil
}

// UnmarshalText accepts only the canonical UTC representation.
func (t *Time) UnmarshalText(data []byte) error {
	parsed, err := time.Parse(Layout, string(data))
	if err != nil || parsed.UTC().Format(Layout) != string(data) {
		return fmt.Errorf("invalid UTC millisecond timestamp %q", data)
	}
	*t = Time(parsed)
	return nil
}

// Format returns the canonical timestamp of instant.
func Format(instant time.Time) string {
	text, _ := Time(instant).MarshalText() // Formatting a time cannot fail.
	return string(text)
}
