// Package timebucket picks the trend granularity shared by the analysis
// reports. Calendar buckets align to local midnights so day, month and year
// boundaries follow the reader's timezone, including DST transitions.
package timebucket

import (
	"iter"
	"time"
)

// maxBuckets bounds one series so a malformed range cannot render forever.
const maxBuckets = 2000

const (
	hourMs = int64(time.Hour / time.Millisecond)
	dayMs  = 24 * hourMs
)

// Bucketing is a trend granularity.
type Bucketing struct {
	Name string // 15m, hour, day, month or year
	step int64  // fixed-step milliseconds; zero for calendar buckets
	loc  *time.Location
}

// For picks the bucket size for a span of epoch milliseconds.
func For(span int64, loc *time.Location) Bucketing {
	switch {
	case span <= 6*hourMs:
		return Bucketing{Name: "15m", step: 15 * 60 * 1000, loc: loc}
	case span <= 72*hourMs:
		return Bucketing{Name: "hour", step: hourMs, loc: loc}
	case span <= 120*dayMs:
		return Bucketing{Name: "day", loc: loc}
	case span <= 10*365*dayMs:
		return Bucketing{Name: "month", loc: loc}
	default:
		return Bucketing{Name: "year", loc: loc}
	}
}

// Start returns the start of the bucket containing ms.
func (b Bucketing) Start(ms int64) int64 {
	t := time.UnixMilli(ms).In(b.loc)
	switch b.Name {
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, b.loc).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, b.loc).UnixMilli()
	case "year":
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, b.loc).UnixMilli()
	default:
		return ms - ms%b.step
	}
}

// Next returns the start of the bucket after the one beginning at start.
func (b Bucketing) Next(start int64) int64 {
	t := time.UnixMilli(start).In(b.loc)
	switch b.Name {
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, b.loc).AddDate(0, 0, 1).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, b.loc).AddDate(0, 1, 0).UnixMilli()
	case "year":
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, b.loc).AddDate(1, 0, 0).UnixMilli()
	default:
		return start + b.step
	}
}

// Spans yields every bucket [start,end) covering [from,to), keeping empty
// buckets for quiet stretches instead of collapsing them.
func (b Bucketing) Spans(from, to int64) iter.Seq2[int64, int64] {
	return func(yield func(int64, int64) bool) {
		last := b.Start(to - 1)
		for start, n := b.Start(from), 0; start <= last && n < maxBuckets; n++ {
			end := b.Next(start)
			if !yield(start, end) {
				return
			}
			start = end
		}
	}
}

// Format renders a bucket boundary in the report's timezone.
func (b Bucketing) Format(ms int64) string {
	return time.UnixMilli(ms).In(b.loc).Format(time.RFC3339)
}
