package framecache

import (
	"encoding/json/v2"
	"errors"
	"math"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/timefmt"
)

// Manifests are a storage boundary. Durations have explicit millisecond units;
// the processing types keep time.Duration only in memory.
type manifest struct {
	Version     int               `json:"version"`
	Key         string            `json:"key"`
	Plan        manifestPlan      `json:"plan"`
	Frames      []manifestFrame   `json:"frames"`
	Failed      []manifestFailure `json:"failed"`
	Warnings    []string          `json:"warnings"`
	LastUsed    timefmt.Time      `json:"last_used"`
	SucceededAt timefmt.Time      `json:"succeeded_at,omitzero"`
	Bytes       int64             `json:"bytes"`
	Checksums   map[string]string `json:"checksums"`
}

type manifestPlan struct {
	DurationMS    int64   `json:"duration_ms"`
	BaseFrames    int     `json:"base_frames"`
	TargetFrames  int     `json:"target_frames"`
	WindowStartMS int64   `json:"window_start_ms"`
	WindowMS      int64   `json:"window_ms"`
	TimesMS       []int64 `json:"times_ms"`
}

type manifestFrame struct {
	PlannedMS    int64  `json:"planned_ms"`
	ActualMS     int64  `json:"actual_ms"`
	Path         string `json:"path"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Bytes        int    `json:"bytes"`
	Recompressed bool   `json:"recompressed"`
}

type manifestFailure struct {
	PlannedMS int64  `json:"planned_ms"`
	ActualMS  int64  `json:"actual_ms"`
	Reason    string `json:"reason"`
}

func marshalRecord(r *record) ([]byte, error) {
	plan := r.Set.Plan
	m := manifest{
		Version: 1, Key: r.Key, Warnings: r.Set.Warn, LastUsed: r.LastUsed,
		SucceededAt: r.SucceededAt, Bytes: r.Bytes, Checksums: r.Checksums,
		Plan: manifestPlan{
			DurationMS: plan.Duration.Milliseconds(), BaseFrames: plan.BaseN, TargetFrames: plan.TargetN,
			WindowStartMS: plan.WindowStart.Milliseconds(), WindowMS: plan.WindowLen.Milliseconds(),
		},
	}
	for _, at := range plan.Times {
		m.Plan.TimesMS = append(m.Plan.TimesMS, at.Milliseconds())
	}
	for _, frame := range r.Set.Frames {
		m.Frames = append(m.Frames, manifestFrame{
			PlannedMS: frame.Planned.Milliseconds(), ActualMS: frame.Actual.Milliseconds(),
			Path: frame.Path, Width: frame.Width, Height: frame.Height, Bytes: frame.Bytes, Recompressed: frame.Recompressed,
		})
	}
	for _, failed := range r.Set.Failed {
		m.Failed = append(m.Failed, manifestFailure{PlannedMS: failed.Planned.Milliseconds(), ActualMS: failed.Actual.Milliseconds(), Reason: failed.Reason})
	}
	return json.Marshal(m)
}

func unmarshalRecord(raw []byte) (record, error) {
	var m manifest
	if err := json.Unmarshal(raw, &m, json.RejectUnknownMembers(true)); err != nil {
		return record{}, err
	}
	if m.Version != 1 || m.LastUsed.IsZero() || len(m.Frames) > 32 || len(m.Plan.TimesMS) > 32 || len(m.Failed) > 32 {
		return record{}, errors.New("invalid frame cache manifest")
	}
	valid := func(value int64) bool { return value >= 0 && value <= math.MaxInt64/int64(time.Millisecond) }
	if !valid(m.Plan.DurationMS) || !valid(m.Plan.WindowStartMS) || !valid(m.Plan.WindowMS) {
		return record{}, errors.New("invalid cached sampling duration")
	}
	r := record{Key: m.Key, LastUsed: m.LastUsed, SucceededAt: m.SucceededAt, Bytes: m.Bytes, Checksums: m.Checksums}
	r.Set.Warn = m.Warnings
	r.Set.Plan = domain.FramePlan{
		Duration: time.Duration(m.Plan.DurationMS) * time.Millisecond, BaseN: m.Plan.BaseFrames, TargetN: m.Plan.TargetFrames,
		WindowStart: time.Duration(m.Plan.WindowStartMS) * time.Millisecond, WindowLen: time.Duration(m.Plan.WindowMS) * time.Millisecond,
	}
	for _, at := range m.Plan.TimesMS {
		if !valid(at) {
			return record{}, errors.New("invalid cached sampling time")
		}
		r.Set.Plan.Times = append(r.Set.Plan.Times, time.Duration(at)*time.Millisecond)
	}
	for _, frame := range m.Frames {
		if !valid(frame.PlannedMS) || !valid(frame.ActualMS) {
			return record{}, errors.New("invalid cached frame time")
		}
		r.Set.Frames = append(r.Set.Frames, domain.Frame{
			Planned: time.Duration(frame.PlannedMS) * time.Millisecond, Actual: time.Duration(frame.ActualMS) * time.Millisecond,
			Path: frame.Path, Width: frame.Width, Height: frame.Height, Bytes: frame.Bytes, Recompressed: frame.Recompressed,
		})
	}
	for _, failure := range m.Failed {
		if !valid(failure.PlannedMS) || !valid(failure.ActualMS) {
			return record{}, errors.New("invalid cached failed sample time")
		}
		r.Set.Failed = append(r.Set.Failed, domain.FailedPoint{Planned: time.Duration(failure.PlannedMS) * time.Millisecond, Actual: time.Duration(failure.ActualMS) * time.Millisecond, Reason: failure.Reason})
	}
	return r, nil
}
