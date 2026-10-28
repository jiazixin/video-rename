package secret

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestDiagnosticsRedactNestedCredentials(t *testing.T) {
	t.Parallel()
	credential := Value("fixture-private-credential")
	value := struct{ Password Value }{credential}
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
		for _, input := range []any{credential, &credential, value, &value} {
			if got := fmt.Sprintf(format, input); strings.Contains(got, credential.Reveal()) {
				t.Fatalf("got leaked credential with %s, want redaction", format)
			}
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("fixture", "credential", credential)
	slog.New(slog.NewJSONHandler(&output, nil)).Info("fixture", "nested", value)
	if strings.Contains(output.String(), credential.Reveal()) {
		t.Fatal("got credential in log, want redaction")
	}
}

func TestInputAndExplicitReveal(t *testing.T) {
	t.Parallel()
	var value Value
	if err := json.Unmarshal([]byte(`"fixture-secret"`), &value); err != nil || value.Reveal() != "fixture-secret" {
		t.Fatalf("got %v, %v, want decoded input", value, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != `"[redacted]"` {
		t.Fatalf("got %s, %v, want redaction", encoded, err)
	}
}
