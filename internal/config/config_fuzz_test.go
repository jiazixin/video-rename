package config

import "testing"

func FuzzConfigurationYAML(f *testing.F) {
	for _, input := range []string{"", "port: 8790", "port: 1\nport: 2", "auth: &a {password: fixture}\nextra: *a", "---\nport: 1\n---\nport: 2"} {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		cfg := defaults()
		// Malformed and recursive YAML must return errors without panicking.
		if err := unmarshalStrict(input, &cfg); err == nil {
			_ = cfg.Validate() // Invalid values are expected parser outcomes.
		}
	})
}
