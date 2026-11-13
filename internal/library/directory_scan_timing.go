package library

import "time"

type scanPhase int

const (
	scanPrepare scanPhase = iota
	scanRead
	scanStage
	scanPublish
	scanCleanup
	scanLockWait
	scanPhaseCount
)

// directoryScanTiming is owned by one mark traversal. Phases are disjoint and
// nested inside refresh_duration_ms; database connection waits stay in the
// phase performing the query, rather than using process-wide pool counters.
type directoryScanTiming [scanPhaseCount]time.Duration

// scanClock changes phase at page boundaries, never at individual files.
// A nil receiver disables instrumentation for scans outside a mark traversal.
type scanClock struct {
	timing  *directoryScanTiming
	phase   scanPhase
	started time.Time
}

func startScanClock(timing *directoryScanTiming) *scanClock {
	if timing == nil {
		return nil
	}
	return &scanClock{timing: timing, phase: scanPrepare, started: time.Now()}
}

func (c *scanClock) enter(phase scanPhase) {
	if c == nil {
		return
	}
	now := time.Now()
	c.timing[c.phase] += now.Sub(c.started)
	c.phase, c.started = phase, now
}

func (c *scanClock) stop() {
	if c != nil {
		c.timing[c.phase] += time.Since(c.started)
	}
}
