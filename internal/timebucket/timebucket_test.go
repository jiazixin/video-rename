package timebucket

import (
	"testing"
	"time"
)

func TestForPicksGranularityBySpan(t *testing.T) {
	for _, tc := range []struct {
		span time.Duration
		want string
	}{
		{6 * time.Hour, "15m"},
		{7 * time.Hour, "hour"},
		{72 * time.Hour, "hour"},
		{73 * time.Hour, "day"},
		{121 * 24 * time.Hour, "month"},
		{11 * 365 * 24 * time.Hour, "year"},
	} {
		if got := For(tc.span.Milliseconds(), time.UTC).Name; got != tc.want {
			t.Errorf("For(%v) = %s, want %s", tc.span, got, tc.want)
		}
	}
}

func TestSpansFollowLocalDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip(err)
	}
	// 2026-03-08 is 23 hours long in New York.
	from := time.Date(2026, 3, 7, 12, 0, 0, 0, loc).UnixMilli()
	to := time.Date(2026, 3, 10, 0, 0, 0, 0, loc).UnixMilli()
	b := For(80*time.Hour.Milliseconds(), loc)
	var got []string
	for start, end := range b.Spans(from, to) {
		got = append(got, b.Format(start)+"/"+b.Format(end))
	}
	want := []string{
		"2026-03-07T00:00:00-05:00/2026-03-08T00:00:00-05:00",
		"2026-03-08T00:00:00-05:00/2026-03-09T00:00:00-04:00",
		"2026-03-09T00:00:00-04:00/2026-03-10T00:00:00-04:00",
	}
	if len(got) != len(want) {
		t.Fatalf("spans = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span %d = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestSpansStopAtLimit(t *testing.T) {
	b := For(time.Hour.Milliseconds(), time.UTC)
	n := 0
	for range b.Spans(0, 100_000*time.Hour.Milliseconds()) {
		n++
	}
	if n != maxBuckets {
		t.Fatalf("spans = %d, want %d", n, maxBuckets)
	}
}
