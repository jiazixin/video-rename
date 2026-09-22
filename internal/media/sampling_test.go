package media

import (
	"os"
	"testing"
	"time"

	"video-rename/internal/config"
)

// loadDefaultSampling 通过加载最小配置获得解析后的默认规则表。
func loadDefaultSampling(t *testing.T) config.SamplingConfig {
	t.Helper()
	cfg, err := config.Load(writeMinimalConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Sampling
}

func writeMinimalConfig(t *testing.T) string {
	t.Helper()
	p := t.TempDir() + "/config.yaml"
	content := "# sampling\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// processing.md 第 2 节的五组默认样例。
func TestPlanDefaultExamples(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	cases := []struct {
		name     string
		duration time.Duration
		size     int64
		wantN    int
	}{
		{"8MiB 20秒 500p", 20 * time.Second, 8 * 1024 * 1024, 3},
		{"120MiB 90秒 1080p", 90 * time.Second, 120 * 1024 * 1024, 5},
		{"3GiB 60秒 4K", 60 * time.Second, 3 * 1024 * 1024 * 1024, 7},
		{"80MiB 1小时 低码率", time.Hour, 80 * 1024 * 1024, 12},
		{"5MiB 0.4秒", 400 * time.Millisecond, 5 * 1024 * 1024, 1},
	}
	for _, tc := range cases {
		plan, err := s.Plan(tc.duration, tc.size)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if plan.TargetN != tc.wantN {
			t.Errorf("%s: N = %d, 期望 %d", tc.name, plan.TargetN, tc.wantN)
		}
		if len(plan.Times) != tc.wantN {
			t.Errorf("%s: 时间点数量 = %d, 期望 %d", tc.name, len(plan.Times), tc.wantN)
		}
	}
}

func TestPlanExactThresholds(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	// 恰好 30s 命中 ≤30s 档(Nd=3);30s+1ms 命中 ≤2m 档(Nd=5)
	plan, _ := s.Plan(30*time.Second, 0)
	if plan.BaseN != 3 {
		t.Errorf("30s 恰在边界: N0 = %d, 期望 3", plan.BaseN)
	}
	plan, _ = s.Plan(30*time.Second+time.Millisecond, 0)
	if plan.BaseN != 5 {
		t.Errorf("30s+1ms: N0 = %d, 期望 5", plan.BaseN)
	}
	// 恰好 50 MiB 命中 ≤50(Ns=3);50MiB+1 命中 ≤200(Ns=5)
	plan, _ = s.Plan(10*time.Second, 50*1024*1024)
	if plan.BaseN != 3 {
		t.Errorf("50MiB 边界: N0 = %d, 期望 3", plan.BaseN)
	}
	plan, _ = s.Plan(10*time.Second, 50*1024*1024+1)
	if plan.BaseN != 5 {
		t.Errorf("50MiB+1: N0 = %d, 期望 5", plan.BaseN)
	}
}

func TestPlanMaxFramesClamp(t *testing.T) {
	sc := loadDefaultSampling(t)
	sc.MaxFrames = 6
	s := NewSampler(sc)
	// processing.md:max_frames=6 时,3GiB/60s 与 80MiB/1h 都限制为 6
	for _, tc := range []struct {
		d    time.Duration
		size int64
	}{{60 * time.Second, 3 << 30}, {time.Hour, 80 << 20}} {
		plan, err := s.Plan(tc.d, tc.size)
		if err != nil {
			t.Fatal(err)
		}
		if plan.TargetN != 6 {
			t.Errorf("max_frames=6 应截断: got %d", plan.TargetN)
		}
	}
}

func TestPlanStrategies(t *testing.T) {
	sc := loadDefaultSampling(t)
	// 3GiB 60秒:size→12,duration→5,hybrid→7
	for strategy, want := range map[string]int{"size": 12, "duration": 5, "hybrid": 7} {
		s2 := sc
		s2.Strategy = strategy
		s2.ParsedStrategy = config.Strategy(strategy)
		s := NewSampler(s2)
		plan, err := s.Plan(60*time.Second, 3<<30)
		if err != nil {
			t.Fatal(err)
		}
		if plan.TargetN != want {
			t.Errorf("策略 %s: N = %d, 期望 %d", strategy, plan.TargetN, want)
		}
	}
}

func TestPlanTimePointsAreMidpoints(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	plan, err := s.Plan(90*time.Second, 120<<20)
	if err != nil {
		t.Fatal(err)
	}
	// A=4.5, Z=85.5, L=81, N=5 → 每段中点
	want := []time.Duration{12600, 28800, 45000, 61200, 77400} // 毫秒
	for i, w := range want {
		if plan.Times[i] != w*time.Millisecond {
			t.Errorf("t[%d] = %v, 期望 %v", i, plan.Times[i], w*time.Millisecond)
		}
	}
	// 全部严格位于窗口内部
	for _, tp := range plan.Times {
		if tp <= 4500*time.Millisecond || tp >= 85500*time.Millisecond {
			t.Errorf("时间点 %v 不在窗口 (4.5s, 85.5s) 内", tp)
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
		t.Error("skip=0 时首个中点应 > 0")
	}
}

func TestPlanRejectsInvalidDuration(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	if _, err := s.Plan(0, 100); err == nil {
		t.Error("0 时长应报错")
	}
	if _, err := s.Plan(-time.Second, 100); err == nil {
		t.Error("负时长应报错")
	}
}

func TestPlanCustomRules(t *testing.T) {
	// 自定义分档:≤1m 4帧,兜底 6帧;验证整体替换
	p := writeCustomConfig(t, `
sampling:
  duration_rules:
    - {max_duration: "1m", frames: 4}
    - {max_duration: null, frames: 6}
`)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSampler(cfg.Sampling)
	plan, _ := s.Plan(30*time.Second, 0)
	if plan.TargetN != 4 {
		t.Errorf("自定义档 ≤1m: N = %d, 期望 4", plan.TargetN)
	}
	plan, _ = s.Plan(5*time.Minute, 0)
	if plan.TargetN != 6 {
		t.Errorf("自定义兜底档: N = %d, 期望 6", plan.TargetN)
	}
}

func writeCustomConfig(t *testing.T, content string) string {
	t.Helper()
	p := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanVeryShortVideoSingleFrame(t *testing.T) {
	s := NewSampler(loadDefaultSampling(t))
	plan, err := s.Plan(300*time.Millisecond, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetN != 1 || len(plan.Times) != 1 {
		t.Fatalf("极短视频应取 1 帧: %+v", plan)
	}
	// 中点位于窗口内
	if plan.Times[0] <= 0 || plan.Times[0] >= 300*time.Millisecond {
		t.Errorf("唯一时间点 %v 不在 (0, 0.3s) 内", plan.Times[0])
	}
}
