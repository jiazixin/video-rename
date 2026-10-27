// Package libraryscan coordinates background discovery without media processing.
package libraryscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"uuid"

	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/systemsettings"
)

// ErrClosed reports that the scan manager is closed.
var ErrClosed = domain.Fail(domain.KindUnavailable, "scan_closed", "directory scan is closed")

// ScanError is a directory that failed to scan. Scope replaces Path for
// failures of the scan settings ("settings") or the scan targets ("targets").
type ScanError struct {
	Path  string        `json:"path,omitempty"`
	Scope string        `json:"scope,omitempty"`
	Error domain.Record `json:"error"`
}

// Status is the state of the current or last scan round.
type Status struct {
	ID          string      `json:"id"`
	Status      string      `json:"status"`
	Source      string      `json:"source"`
	Targets     []int64     `json:"targets"`
	Started     string      `json:"started"`
	Finished    string      `json:"finished"`
	NextScan    string      `json:"next_scan"`
	CurrentPath string      `json:"current_path"`
	Directories int64       `json:"directories"`
	Files       int64       `json:"files"`
	Added       int64       `json:"added"`
	Changed     int64       `json:"changed"`
	Removed     int64       `json:"removed"`
	Errors      []ScanError `json:"errors"`
}

type scanTimer interface {
	C() <-chan time.Time
	Stop()
}

type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }
func (t realTimer) Stop()               { t.Timer.Stop() }

// Manager runs background library scans.
type Manager struct {
	library  *library.Library
	settings systemsettings.Store

	mu            sync.Mutex
	status        Status
	active        bool
	closed        bool
	loopStarted   bool
	cancel        context.CancelFunc
	anchor        time.Time
	settingsError error
	wake          chan struct{}
	closedCh      chan struct{}
	wg            sync.WaitGroup

	// Hooks keep scheduling and in-flight NAS calls testable without waiting an hour.
	now      func() time.Time
	newTimer func(time.Duration) scanTimer
	scan     func(context.Context, int64) (library.ScanDirectoryResult, error)
}

// New creates a scan manager for l with settings.
func New(l *library.Library, settings systemsettings.Store) *Manager {
	return &Manager{
		library: l, settings: settings,
		status: Status{Status: "idle", Targets: []int64{}, Errors: []ScanError{}},
		wake:   make(chan struct{}, 1), closedCh: make(chan struct{}), now: time.Now,
		newTimer: func(d time.Duration) scanTimer { return realTimer{time.NewTimer(d)} },
	}
}

func (m *Manager) snapshotLocked() Status {
	s := m.status
	s.Targets = slices.Clone(s.Targets)
	s.Errors = slices.Clone(s.Errors)
	if m.settingsError != nil {
		s.Errors = append(s.Errors, ScanError{Scope: "settings", Error: domain.RecordOf(m.settingsError)})
		if !m.active {
			s.Status = "failed"
		}
	}
	return s
}

// Snapshot returns the current scan status.
func (m *Manager) Snapshot() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// SettingsChanged resets the interval from the saved configuration change.
// An active round finishes first and then starts a complete new interval.
func (m *Manager) SettingsChanged() {
	m.mu.Lock()
	m.anchor = m.now()
	m.status.NextScan = ""
	m.mu.Unlock()
	m.notify()
}

// ValidateTargets rejects unavailable or non-directory targets before removing
// duplicate and overlapping scopes. Every requested path is checked, including
// children that will subsequently be covered by a selected ancestor.
func (m *Manager) ValidateTargets(ctx context.Context, targets []int64) ([]int64, error) {
	if len(targets) == 0 || len(targets) > 100 {
		return nil, domain.InvalidField("targets", "selection_required")
	}
	selected := make(map[int64]bool, len(targets))
	unique := make([]int64, 0, len(targets))
	ancestors := make(map[int64][]int64, len(targets))
	for _, id := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if selected[id] {
			continue
		}
		e, err := m.library.IndexedEntry(id)
		if err != nil || id <= 0 || e.Kind != "directory" {
			return nil, domain.InvalidField("targets", "not_directory")
		}
		if _, err = m.library.Path(id); err != nil {
			return nil, domain.Fail(domain.KindUnavailable, "scan_target_unavailable", "scan target is unavailable", "path", m.library.DisplayPath(id)).Wrap(err)
		}
		seen := map[int64]bool{id: true}
		for parent := e.Parent; parent > 0; {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if seen[parent] || len(seen) > 1024 {
				return nil, domain.Fail(domain.KindConflict, "invalid_hierarchy", "directory hierarchy is invalid")
			}
			seen[parent] = true
			ancestors[id] = append(ancestors[id], parent)
			e, err = m.library.IndexedEntry(parent)
			if err != nil || e.Kind != "directory" {
				return nil, domain.Fail(domain.KindConflict, "scan_hierarchy_changed", "scan directory hierarchy changed; refresh")
			}
			parent = e.Parent
		}
		selected[id] = true
		unique = append(unique, id)
	}
	return slices.DeleteFunc(unique, func(id int64) bool {
		for _, parent := range ancestors[id] {
			if selected[parent] {
				return true
			}
		}
		return false
	}), ctx.Err()
}

// Start runs under the supplied application lifecycle context. HTTP callers
// should pass that context so client disconnection does not stop discovery.
func (m *Manager) Start(ctx context.Context, targets []int64, source string) (Status, error) {
	m.mu.Lock()
	if m.closed || m.active {
		s, closed := m.snapshotLocked(), m.closed
		m.mu.Unlock()
		if closed {
			return s, ErrClosed
		}
		return s, nil
	}
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return m.Snapshot(), err
	}
	settings, err := m.settings.Scan()
	if err != nil {
		m.mu.Lock()
		m.settingsError = err
		m.status.NextScan = ""
		m.mu.Unlock()
		m.notify()
		return m.Snapshot(), domain.Fail(domain.KindInternal, "scan_settings_unreadable", "read scan settings").Wrap(err)
	}
	if len(targets) == 0 {
		targets = settings.Targets
	}
	targets, err = m.ValidateTargets(ctx, targets)
	if err != nil {
		return m.Snapshot(), err
	}
	if source == "" {
		source = "manual"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.snapshotLocked(), ErrClosed
	}
	if m.active {
		return m.snapshotLocked(), nil
	}
	if err := ctx.Err(); err != nil {
		return m.snapshotLocked(), err
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel, m.active = cancel, true
	m.status = Status{ID: uuid.New().String(), Status: "scanning", Source: source,
		Targets: slices.Clone(targets), Started: m.now().UTC().Format(time.RFC3339Nano), Errors: []ScanError{}}
	m.wg.Go(func() { m.run(runCtx, targets, settings.ExcludedTargets) })
	m.notify()
	return m.snapshotLocked(), nil
}

// Cancel cancels the running scan round and returns the resulting status.
func (m *Manager) Cancel() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active {
		m.status.Status = "cancelling"
		m.cancel()
	}
	return m.snapshotLocked()
}

// Close cancels scanning and waits for it to stop.
func (m *Manager) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.status.NextScan = ""
		close(m.closedCh)
		if m.active {
			m.status.Status = "cancelling"
			m.cancel()
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func nextScanAt(anchor time.Time, settings systemsettings.ScanSettings) time.Time {
	if !settings.Enabled {
		return time.Time{}
	}
	return anchor.Add(time.Duration(settings.IntervalMinutes) * time.Minute)
}

// Loop waits a full configured interval on startup and after every completed
// round. Settings and completion notifications replan one timer; no polling of
// the NAS is needed while waiting. Failed settings reads suspend automatic work
// until a saved configuration change, and remain visible in Snapshot.
func (m *Manager) Loop(ctx context.Context) {
	m.mu.Lock()
	if m.closed || m.loopStarted {
		m.mu.Unlock()
		return
	}
	m.loopStarted = true
	if m.anchor.IsZero() {
		m.anchor = m.now()
	}
	// Registration is under the same closed gate used by Start and Close.
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.status.NextScan = ""
		m.mu.Unlock()
		m.wg.Done()
	}()
	var timer scanTimer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		settings, err := m.settings.Scan()
		m.mu.Lock()
		m.status.NextScan = ""
		var at time.Time
		if err != nil {
			m.settingsError = domain.Fail(domain.KindInternal, "scan_settings_unreadable", "read scan settings").Wrap(err)
		} else {
			m.settingsError = nil
			if !m.active && !m.closed {
				at = nextScanAt(m.anchor, settings)
			}
		}
		if !at.IsZero() {
			m.status.NextScan = at.UTC().Format(time.RFC3339Nano)
		}
		m.mu.Unlock()
		var timerCh <-chan time.Time
		if !at.IsZero() {
			timer = m.newTimer(max(time.Duration(0), at.Sub(m.now())))
			timerCh = timer.C()
		}
		select {
		case <-ctx.Done():
			m.Cancel()
			return
		case <-m.closedCh:
			return
		case <-m.wake:
			continue
		case <-timerCh:
			// A save may race the expired timer; honor its new interval/enablement.
			settings, err = m.settings.Scan()
			if err != nil {
				continue
			}
			m.mu.Lock()
			due := nextScanAt(m.anchor, settings)
			ready := !m.closed && !m.active && !due.IsZero() && !m.now().Before(due)
			m.mu.Unlock()
			if !ready {
				continue
			}
			if _, err := m.Start(ctx, settings.Targets, "automatic"); err != nil {
				m.mu.Lock()
				if !m.active && !m.closed && ctx.Err() == nil {
					m.status = Status{Status: "failed", Source: "automatic", Targets: slices.Clone(settings.Targets),
						Finished: m.now().UTC().Format(time.RFC3339Nano),
						Errors:   []ScanError{{Scope: "targets", Error: domain.RecordOf(err)}}}
					m.anchor = m.now()
				}
				m.mu.Unlock()
			}
		}
	}
}

type directoryCursor struct {
	id        int64
	after     int64
	children  []int64
	next      int
	exhausted bool
}

// nextChild traverses only a parent's successfully published index. Its keyset
// cursor retains at most one 128-child page per depth, even for a wide NAS tree.
func (m *Manager) nextChild(ctx context.Context, cursor *directoryCursor) (int64, error) {
	if cursor.next == len(cursor.children) {
		if cursor.exhausted {
			return 0, sql.ErrNoRows
		}
		rows, err := m.library.DB.Read().QueryContext(ctx, `SELECT i.entry FROM directory_items i
 JOIN directory_state s ON s.dir=i.dir AND s.generation=i.generation
 JOIN entries e ON e.id=i.entry AND e.present=1 AND e.kind='directory'
 WHERE i.dir=? AND i.kind='directory' AND i.entry>? ORDER BY i.entry LIMIT 128`, cursor.id, cursor.after)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		cursor.children, cursor.next = cursor.children[:0], 0
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return 0, err
			}
			cursor.children = append(cursor.children, id)
		}
		if err := rows.Err(); err != nil {
			return 0, err
		}
		cursor.exhausted = len(cursor.children) < 128
		if len(cursor.children) == 0 {
			return 0, sql.ErrNoRows
		}
	}
	id := cursor.children[cursor.next]
	cursor.next++
	cursor.after = id
	return id, nil
}

func (m *Manager) addError(id int64, err error) {
	path := m.library.DisplayPath(id)
	m.mu.Lock()
	m.status.Errors = append(m.status.Errors, ScanError{Path: path, Error: domain.RecordOf(err)})
	m.mu.Unlock()
}

// scanOne validates the root before each directory and again after a failed
// read. A directory-specific error skips its descendants but not other ranges;
// a missing or replaced root stops the whole round before any further work.
func (m *Manager) scanOne(ctx context.Context, id int64, scan func(context.Context, int64) (library.ScanDirectoryResult, error)) (success, fatal bool) {
	if ctx.Err() != nil {
		return false, false
	}
	if _, err := m.library.Path(1); err != nil {
		m.addError(1, err)
		return false, true
	}
	path := m.library.DisplayPath(id)
	m.mu.Lock()
	m.status.CurrentPath = path
	m.mu.Unlock()
	result, err := scan(ctx, id)
	if errors.Is(err, library.ErrDirectoryScanChanged) && ctx.Err() == nil {
		// Retry the complete scan once so a transient NAS observation can settle.
		// A missing or replaced root must stop the round before another read.
		if _, rootErr := m.library.Path(1); rootErr == nil && ctx.Err() == nil {
			firstErr := err
			result, err = scan(ctx, id)
			if err != nil {
				err = fmt.Errorf("retried scan: %w; first scan: %w", err, firstErr)
			}
		}
	}
	if err != nil && ctx.Err() != nil {
		return false, false
	}
	if err != nil {
		m.addError(id, err)
		if _, rootErr := m.library.Path(1); rootErr != nil {
			if id != 1 {
				m.addError(1, rootErr)
			}
			return false, true
		}
		return false, id == 1
	}
	m.mu.Lock()
	m.status.Directories++
	m.status.Files += result.Files
	m.status.Added += result.Added
	m.status.Changed += result.Changed
	m.status.Removed += result.Removed
	m.mu.Unlock()
	return true, false
}

func (m *Manager) run(ctx context.Context, targets, excluded []int64) {
	fatal := false
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		cancelled := ctx.Err() != nil
		m.cancel()
		m.cancel, m.active = nil, false
		switch {
		case cancelled && !fatal:
			m.status.Status = "cancelled"
		case fatal || len(m.status.Errors) > 0 && m.status.Directories == 0:
			m.status.Status = "failed"
		case len(m.status.Errors) > 0:
			m.status.Status = "partial_failure"
		default:
			m.status.Status = "completed"
		}
		m.anchor = m.now()
		m.status.Finished = m.anchor.UTC().Format(time.RFC3339Nano)
		m.notify()
	}()
	scan := m.scan
	if scan == nil {
		scanner, err := m.library.NewDirectoryScanner(ctx)
		if err != nil {
			m.addError(targets[0], err)
			return
		}
		defer scanner.Close()
		scan = scanner.ScanDirectory
	}
	for _, target := range targets {
		skip, err := m.excluded(ctx, target, excluded)
		if err != nil {
			m.addError(target, err)
			continue
		}
		if skip {
			continue
		}
		ok, stop := m.scanOne(ctx, target, scan)
		if stop {
			fatal = true
			return
		}
		if ctx.Err() != nil {
			return
		}
		if !ok {
			continue
		}
		stack := []directoryCursor{{id: target}}
		for len(stack) > 0 {
			if ctx.Err() != nil {
				return
			}
			cursor := &stack[len(stack)-1]
			child, err := m.nextChild(ctx, cursor)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if !errors.Is(err, sql.ErrNoRows) {
					m.addError(cursor.id, err)
				}
				stack = stack[:len(stack)-1]
				continue
			}
			if len(stack) >= 1024 {
				m.addError(child, domain.Fail(domain.KindInvalid, "scan_too_deep", "directory tree deeper than 1024 levels"))
				continue
			}
			skip, err := m.excluded(ctx, child, excluded)
			if err != nil {
				m.addError(child, err)
				continue
			}
			if skip {
				continue
			}
			ok, stop = m.scanOne(ctx, child, scan)
			if stop {
				fatal = true
				return
			}
			if ok {
				stack = append(stack, directoryCursor{id: child})
			}
		}
	}
}

// excluded checks indexed ancestors without opening the excluded directory.
func (m *Manager) excluded(ctx context.Context, id int64, excluded []int64) (bool, error) {
	if len(excluded) == 0 {
		return false, nil
	}
	seen := map[int64]bool{}
	for id > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if slices.Contains(excluded, id) {
			return true, nil
		}
		if seen[id] || len(seen) >= 1024 {
			return false, domain.Fail(domain.KindConflict, "invalid_hierarchy", "directory hierarchy is invalid")
		}
		seen[id] = true
		e, err := m.library.IndexedEntry(id)
		if err != nil {
			return false, err
		}
		id = e.Parent
	}
	return false, nil
}
