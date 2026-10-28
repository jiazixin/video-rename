package media

import (
	"testing"

	"video-rename/internal/domain"
)

func FuzzProbeJSON(f *testing.F) {
	for _, input := range []string{`{}`, `{"streams":[{"codec_type":"video","width":640,"height":360,"duration":"5"}]}`, `{"streams":null}`, `{"format":{"duration":"NaN"}}`} {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		meta, err := parseProbeOutput(input, domain.VideoJob{Path: "fixture.mp4"})
		if err == nil && (meta.Width <= 0 || meta.Height <= 0 || meta.Duration <= 0) {
			t.Fatalf("got invalid media metadata %+v, want positive dimensions and duration", meta)
		}
	})
}
