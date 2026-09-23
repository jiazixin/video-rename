package media

import (
	"fmt"
	"math"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
)

// Sampler 根据 sampling 配置生成抽帧计划。
type Sampler struct {
	Cfg config.SamplingConfig
}

// NewSampler 用已校验的配置构造采样器。
func NewSampler(cfg config.SamplingConfig) *Sampler { return &Sampler{Cfg: cfg} }

// Plan 计算 docs/processing.md 第 2 节定义的抽帧计划:
//
//	Nd 来自时长表,Ns 来自大小表;hybrid 取 N0 = Nd + min(2, max(0, Ns−Nd)),
//	size 取 N0 = Ns,duration 取 N0 = Nd。
//	窗口 A = D×skip_start,Z = D×(1−skip_end),L = Z−A;
//	N = min(N0, max_frames, max(1, floor(L/1s)));
//	t[i] = A + (i+0.5)×L/N。
func (s *Sampler) Plan(duration time.Duration, sizeBytes int64) (plan domain.FramePlan, err error) {
	if duration <= 0 {
		return plan, fmt.Errorf("时长必须大于 0: %v", duration)
	}
	if math.IsNaN(float64(duration)) || math.IsInf(float64(duration), 0) {
		return plan, fmt.Errorf("时长不是有限值")
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
		return plan, fmt.Errorf("有效采样窗口非正: %v", l)
	}
	n := min(n0, s.Cfg.MaxFrames, max(1, int(math.Floor(l))))
	if n < 1 {
		n = 1
	}
	plan.Duration = duration
	plan.BaseN = n0
	plan.TargetN = n
	plan.WindowStart = time.Duration(a * float64(time.Second))
	plan.WindowLen = time.Duration(l * float64(time.Second))
	plan.Times = make([]time.Duration, n)
	for i := range n {
		t := a + (float64(i)+0.5)*l/float64(n)
		if !isFinite(t) {
			return plan, fmt.Errorf("无法生成有效时间点 %d", i)
		}
		plan.Times[i] = time.Duration(t * float64(time.Second))
	}
	return plan, nil
}

// pickDurationFrames 取首个"时长 ≤ 上限"的档位帧数。
func pickDurationFrames(rules []config.ParsedDurationRule, d time.Duration) int {
	for _, r := range rules {
		if d <= r.Max {
			return r.Frames
		}
	}
	return rules[len(rules)-1].Frames // 兜底档
}

// pickSizeFrames 取首个"字节数 ≤ 上限"的档位帧数。
func pickSizeFrames(rules []config.ParsedSizeRule, size int64) int {
	for _, r := range rules {
		if size <= r.Max {
			return r.Frames
		}
	}
	return rules[len(rules)-1].Frames
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
