package main

import (
	"testing"
	"time"
)

func TestApplicationBuildMetadata(t *testing.T) {
	t.Parallel()
	input := buildInfo{Version: "v1.0.0", Revision: "abcdef123456", Time: "2026-10-28T04:00:00Z", Modified: true}
	got, err := applicationBuild(input)
	wantTime := time.Date(2026, 10, 28, 4, 0, 0, 0, time.UTC)
	if err != nil || got.Version != input.Version || got.Revision != input.Revision || got.Modified != input.Modified || !time.Time(got.Time).Equal(wantTime) {
		t.Fatalf("got %+v, err=%v, want complete metadata for %s", got, err, input.Time)
	}
	input.Time = ""
	if got, err := applicationBuild(input); err != nil || !got.Time.IsZero() {
		t.Fatalf("got timestamp=%v err=%v, want absent build time", got.Time, err)
	}
	input.Time = "invalid"
	if _, err := applicationBuild(input); err == nil {
		t.Fatal("got no error, want malformed build metadata rejected")
	}
}
