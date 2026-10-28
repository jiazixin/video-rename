package media

import (
	"testing"
	"time"

	"video-rename/internal/config"
)

// loadDefaultSampling validates the same defaults used by persisted frame settings.
func loadDefaultSampling(t *testing.T) config.SamplingConfig {
	t.Helper()
	settings := config.DefaultFrameSettings()
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	return settings.Sampling
}

// the five default examples from processing.md section 2.
func TestPlanDefaultExamples(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	cases := []struct {
		name     string
		duration time.Duration
		size     int64
		wantN    int
	}{
		{"8MiB 20秒 500p", 20 * time.Second, 8 * 1024 * 1024, 8},
		{"120MiB 90秒 1080p", 90 * time.Second, 120 * 1024 * 1024, 12},
		{"3GiB 60秒 4K", 60 * time.Second, 3 * 1024 * 1024 * 1024, 12},
		{"80MiB 1小时 低码率", time.Hour, 80 * 1024 * 1024, 32},
		{"5MiB 0.4秒", 400 * time.Millisecond, 5 * 1024 * 1024, 1},
	}
	for _, tc := range cases {
		plan, err := s.Plan(tc.duration, tc.size)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if plan.TargetN != tc.wantN {
			t.Errorf("%s: N = %d, want %d", tc.name, plan.TargetN, tc.wantN)
		}
		if len(plan.Times) != tc.wantN {
			t.Errorf("%s: time pointcount = %d, want %d", tc.name, len(plan.Times), tc.wantN)
		}
	}
}

func TestPlanExactThresholds(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	for _, tc := range []struct {
		duration time.Duration
		want     int
	}{
		{30 * time.Second, 8},
		{30*time.Second + time.Millisecond, 12},
		{2 * time.Minute, 12},
		{2*time.Minute + time.Millisecond, 24},
		{10 * time.Minute, 24},
		{10*time.Minute + time.Millisecond, 32},
		{30 * time.Minute, 32},
		{30*time.Minute + time.Millisecond, 32},
	} {
		plan, err := s.Plan(tc.duration, 0)
		if err != nil || plan.BaseN != tc.want || plan.TargetN != tc.want {
			t.Errorf("duration %v: plan=%+v, err=%v, want %d frame", tc.duration, plan, err, tc.want)
		}
	}
	sc := loadDefaultSampling(t)
	sc.ParsedStrategy = config.StrategySize
	s = NewSampler(sc)
	// exactly 50 MiB hits ≤50 (Ns=3); 50MiB+1 hits ≤200 (Ns=5)
	plan, _ := s.Plan(10*time.Second, 50*1024*1024)
	if plan.BaseN != 3 {
		t.Errorf("50MiB boundary: N0 = %d, want 3", plan.BaseN)
	}
	plan, _ = s.Plan(10*time.Second, 50*1024*1024+1)
	if plan.BaseN != 5 {
		t.Errorf("50MiB+1: N0 = %d, want 5", plan.BaseN)
	}
}

func TestPlanMaxFramesClamp(t *testing.T) {
	sc := loadDefaultSampling(t)
	sc.MaxFrames = 6
	s := NewSampler(sc)
	// processing.md: with max_frames=6, both 3GiB/60s and 80MiB/1h are limited to 6
	for _, tc := range []struct {
		d    time.Duration
		size int64
	}{{60 * time.Second, 3 << 30}, {time.Hour, 80 << 20}} {
		plan, err := s.Plan(tc.d, tc.size)
		if err != nil {
			t.Fatal(err)
		}
		if plan.TargetN != 6 {
			t.Errorf("max_frames=6 should truncate: got %d", plan.TargetN)
		}
	}
}

func TestPlanStrategies(t *testing.T) {
	sc := loadDefaultSampling(t)
	// 3GiB 20s: size→12, duration→8, hybrid→10
	for strategy, want := range map[string]int{"size": 12, "duration": 8, "hybrid": 10} {
		s2 := sc
		s2.Strategy = strategy
		s2.ParsedStrategy = config.Strategy(strategy)
		s := NewSampler(s2)
		plan, err := s.Plan(20*time.Second, 3<<30)
		if err != nil {
			t.Fatal(err)
		}
		if plan.TargetN != want {
			t.Errorf("strategy %s: N = %d, want %d", strategy, plan.TargetN, want)
		}
	}
}

func TestPlanTimePointsAreMidpoints(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	plan, err := s.Plan(90*time.Second, 120<<20)
	if err != nil {
		t.Fatal(err)
	}
	// A=4.5, Z=85.5, L=81, N=12 → segment midpoints
	want := []time.Duration{7875, 14625, 21375, 28125, 34875, 41625, 48375, 55125, 61875, 68625, 75375, 82125} // milliseconds
	for i, w := range want {
		if plan.Times[i] != w*time.Millisecond {
			t.Errorf("t[%d] = %v, want %v", i, plan.Times[i], w*time.Millisecond)
		}
	}
	// all strictly inside the window
	for _, tp := range plan.Times {
		if tp <= 4500*time.Millisecond || tp >= 85500*time.Millisecond {
			t.Errorf("time point %v outside (4.5s, 85.5s) window", tp)
		}
	}
}

func TestPlanSkipZero(t *testing.T) {
	sc := loadDefaultSampling(t)
	sc.SkipStart = 0
	sc.SkipEnd = 0
	s := NewSampler(sc)
	plan, err := s.Plan(10*time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Times[0] <= 0 {
		t.Error("first midpoint should be > 0 when skip=0")
	}
}

func TestPlanRejectsInvalidDuration(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	if _, err := s.Plan(0, 100); err == nil {
		t.Error("0 durationshould fail")
	}
	if _, err := s.Plan(-time.Second, 100); err == nil {
		t.Error("negative duration should fail")
	}
}

func TestPlanCustomRules(t *testing.T) {
	// custom tiers: ≤1m 4 frames, fallback 6 frames; the whole table is replaced
	settings := config.DefaultFrameSettings()
	limit := "1m"
	settings.Sampling.DurationRules = []config.DurationRule{{MaxDuration: &limit, Frames: 4}, {Frames: 6}}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	s := NewSampler(settings.Sampling)
	plan, _ := s.Plan(30*time.Second, 0)
	if plan.TargetN != 4 {
		t.Errorf("custom rule <=1m: N = %d, want 4", plan.TargetN)
	}
	plan, _ = s.Plan(5*time.Minute, 0)
	if plan.TargetN != 6 {
		t.Errorf("custom fallback rule: N = %d, want 6", plan.TargetN)
	}
}
