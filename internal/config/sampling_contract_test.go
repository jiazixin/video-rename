package config

import (
	"math"
	"strconv"
	"testing"
)

func TestDurationRuleRejectsNonpositiveAndOverflowingMilliseconds(t *testing.T) {
	for _, limit := range []int64{0, -1, -9223372036855, math.MinInt64, math.MaxInt64} {
		t.Run(strconv.FormatInt(limit, 10), func(t *testing.T) {
			settings := DefaultFrameSettings()
			settings.Sampling.DurationRules = []DurationRule{{MaxDurationMS: new(limit), Frames: 4}, {Frames: 8}}
			if err := settings.Validate(); err == nil {
				t.Fatalf("accepted invalid duration %d ms", limit)
			}
		})
	}
}
