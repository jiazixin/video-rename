package media

import (
	"fmt"
	"math"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

// Sampler builds frame extraction plans from the sampling settings.
type Sampler struct {
	Cfg config.SamplingConfig
}

// NewSampler builds a sampler from validated settings.
func NewSampler(cfg config.SamplingConfig) *Sampler { return &Sampler{Cfg: cfg} }

// Plan computes the extraction plan defined in docs/processing.md section 2:
//
//	Nd comes from the duration table, Ns from the size table; hybrid uses N0 = Nd + min(2, max(0, Ns−Nd)),
//	size uses N0 = Ns, duration uses N0 = Nd.
//	window A = D×skip_start, Z = D×(1−skip_end), L = Z−A;
//	N = min(N0, max_frames, max(1, floor(L/1s)));
//	t[i] = A + (i+0.5)×L/N.
func (s *Sampler) Plan(duration time.Duration, sizeBytes int64) (plan domain.FramePlan, err error) {
	if duration <= 0 {
		return plan, fmt.Errorf("duration must be positive: %v", duration)
	}
	nd := pickDurationFrames(s.Cfg.ParsedDuration, duration)
	ns := pickSizeFrames(s.Cfg.ParsedSize, sizeBytes)
	var n0 int
	switch s.Cfg.ParsedStrategy {
	case config.StrategySize:
		n0 = ns
	case config.StrategyDuration:
		n0 = nd
	default: // hybrid
		n0 = nd + min(2, max(0, ns-nd))
	}

	d := float64(duration) / float64(time.Second)
	a := d * s.Cfg.SkipStart
	z := d * (1 - s.Cfg.SkipEnd)
	l := z - a
	if !isFinite(l) || l <= 0 {
		return plan, fmt.Errorf("sampling window is not positive: %v", l)
	}
	n := max(min(n0, s.Cfg.MaxFrames, max(1, int(math.Floor(l)))), 1)
	plan.Duration = duration
	plan.BaseN = n0
	plan.TargetN = n
	plan.WindowStart = time.Duration(a * float64(time.Second))
	plan.WindowLen = time.Duration(l * float64(time.Second))
	plan.Times = make([]time.Duration, n)
	for i := range n {
		t := a + (float64(i)+0.5)*l/float64(n)
		if !isFinite(t) {
			return plan, fmt.Errorf("cannot place sample point %d", i)
		}
		plan.Times[i] = time.Duration(t * float64(time.Second))
	}
	return plan, nil
}

// pickDurationFrames returns the frames of the first tier whose limit is at least the duration.
func pickDurationFrames(rules []config.ParsedDurationRule, d time.Duration) int {
	for _, r := range rules {
		if d <= r.Max {
			return r.Frames
		}
	}
	return rules[len(rules)-1].Frames // fallback tier
}

// pickSizeFrames returns the frames of the first tier whose limit is at least the byte size.
func pickSizeFrames(rules []config.ParsedSizeRule, size int64) int {
	for _, r := range rules {
		if size <= r.Max {
			return r.Frames
		}
	}
	return rules[len(rules)-1].Frames
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
