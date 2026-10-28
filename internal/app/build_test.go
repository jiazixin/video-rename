package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"video-rename/internal/timefmt"
)

func TestInfoBuildTimestamp(t *testing.T) {
	t.Parallel()
	for _, known := range []bool{false, true} {
		a := &App{build: Build{Version: "test", Revision: "abcdef123456", Modified: true}}
		if known {
			a.build.Time = timefmt.Time(time.Date(2026, 10, 28, 12, 0, 0, 123456789, time.FixedZone("fixture", 8*60*60)))
		}
		w := httptest.NewRecorder()
		a.info(w, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result["version"] != "test" || result["revision"] != "abcdef123456" || result["modified"] != true {
			t.Fatalf("got %+v, want complete build identity", result)
		}
		at, exists := result["build_time"]
		if exists != known || (known && at != "2026-10-28T04:00:00.123Z") {
			t.Fatalf("got build_time=%v exists=%v, want known=%v with UTC milliseconds", at, exists, known)
		}
	}
}
