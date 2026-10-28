// Package secret prevents credentials from appearing in formatted diagnostics.
package secret

import "log/slog"

// Value contains a credential. Serialization is reserved for the explicit
// configuration, encrypted persistence and outbound authentication boundaries.
type Value string

// Reveal returns the credential for an explicit authentication boundary.
func (v Value) Reveal() string { return string(v) }

// String redacts credentials in ordinary formatted output.
func (v Value) String() string { return "[redacted]" }

// GoString redacts credentials in Go-syntax formatted output.
func (v Value) GoString() string { return v.String() }

// LogValue redacts credentials in structured logs.
func (v Value) LogValue() slog.Value { return slog.StringValue(v.String()) }

// MarshalJSON also redacts nested credentials when a structured logger encodes
// its enclosing object. Encrypted persistence must explicitly call Reveal.
func (v Value) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
