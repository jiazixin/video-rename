package job

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/library"
	"video-rename/internal/systemsetting"
)

func TestProcessedMarkAndCreateInterleavings(t *testing.T) {
	for _, tc := range []struct {
		name         string
		createBefore bool
		createAfter  bool
		recursive    bool
		hardLink     bool
		workerStage  string
	}{
		{name: "queued before occupancy check", createBefore: true},
		{name: "created after mark commit", createAfter: true},
		{name: "file queued after occupancy checks"},
		{name: "ancestor queued after occupancy checks", recursive: true},
		{name: "hard link queued after occupancy checks", hardLink: true},
		{name: "candidate queued before mark commit", workerStage: "queued"},
		{name: "analysis started before mark commit", workerStage: "analyzing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMarkConcurrencyFixture(t)
			m, file, guard := fixture.manager, fixture.file, fixture.guard
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			var workers sync.WaitGroup
			defer workers.Wait()
			defer cancel()

			markReached, releaseMark := make(chan bool, 1), make(chan struct{})
			// The two files share a mark batch. Pause before the first check or
			// after the last real Busy result, without replacing its decision.
			m.Library.Busy = func(ctx context.Context, id int64) bool {
				if tc.createBefore && id == file.ID {
					markReached <- false
					select {
					case <-releaseMark:
					case <-ctx.Done():
						return true
					}
				}
				busy := m.Busy(ctx, id)
				if !tc.createBefore && id == guard.ID {
					markReached <- busy
					select {
					case <-releaseMark:
					case <-ctx.Done():
						return true
					}
				}
				return busy
			}
			workerReached, releaseWorker := make(chan struct{}, 1), make(chan time.Time)
			if tc.workerStage == "queued" {
				settings, err := systemsetting.New(m.db).Get(ctx)
				if err != nil {
					t.Fatal(err)
				}
				settings.StabilitySeconds = 1
				if err := systemsetting.New(m.db).Save(ctx, settings); err != nil {
					t.Fatal(err)
				}
				// Execute reaches this only after persisting a queued candidate.
				m.after = func(time.Duration) <-chan time.Time {
					workerReached <- struct{}{}
					return releaseWorker
				}
			}
			var modelCalls atomic.Int32
			m.analyzeTest = func(ctx context.Context, _ *config.TaskConfig, _ domain.VideoJob, _, _ string) (string, error) {
				modelCalls.Add(1)
				if tc.workerStage == "analyzing" {
					workerReached <- struct{}{}
					select {
					case <-releaseWorker:
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return "renamed", nil
			}

			markDone := make(chan library.MarkResult, 1)
			workers.Go(func() {
				markDone <- m.Library.SetProcessed(ctx, []int64{file.ID, guard.ID}, true)
			})
			if busy := awaitMarkEvent(t, ctx, markReached); busy {
				t.Fatal("got busy fixture, want no queued run yet")
			}
			fixture.assertSources(t, "", "")
			var marks library.MarkResult
			if tc.createAfter {
				close(releaseMark)
				marks = awaitMarkEvent(t, ctx, markDone)
			}
			target := file.ID
			if tc.recursive {
				target = file.Parent
			} else if tc.hardLink {
				target = markEntryNamed(t, m, "alias.mp4").ID
			}
			type createResult struct {
				run int64
				err error
			}
			created := make(chan createResult, 1)
			workers.Go(func() {
				run, err := m.Create(ctx, Request{Targets: []int64{target}, Recursive: tc.recursive, Key: "mark-create-interleaving"}, "manual", 0)
				created <- createResult{run: run, err: err}
			})
			creation := awaitMarkEvent(t, ctx, created)
			if creation.err != nil {
				t.Fatal(creation.err)
			}
			run := creation.run
			if got := readRun(t, m, run).Status; got != "queued" {
				t.Fatalf("got run status %q, want queued", got)
			}
			if !m.Busy(ctx, file.ID) {
				t.Fatal("got unoccupied file after Create, want occupied")
			}
			runDone := make(chan struct{})
			startRun := func() {
				workers.Go(func() {
					defer close(runDone)
					m.Execute(ctx, run)
				})
			}
			if tc.workerStage != "" {
				startRun()
				awaitMarkEvent(t, ctx, workerReached)
				status := "queued"
				if tc.workerStage == "analyzing" {
					status = "running"
				}
				fixture.assertRunItem(t, markRunItemExpectation{run: run, status: status})
				fixture.assertSources(t, "", "")
			}
			if !tc.createAfter {
				close(releaseMark)
				marks = awaitMarkEvent(t, ctx, markDone)
			}
			wantChanged, wantFailed := int64(2), 0
			if tc.createBefore {
				wantChanged, wantFailed = 1, 1
			}
			if marks.Changed != wantChanged || marks.Failed != wantFailed || marks.Unchanged != 0 || marks.Interrupted || len(marks.Failures) != wantFailed {
				t.Fatalf("got marks %+v, want changed=%d failed=%d without interruption", marks, wantChanged, wantFailed)
			}
			if tc.createBefore && (marks.Failures[0].ID != file.ID || marks.Failures[0].Error.Code != "entry_busy") {
				t.Fatalf("got mark failure %+v, want target entry_busy", marks.Failures[0])
			}
			source := "marked"
			if tc.createBefore {
				source = ""
			}
			fixture.assertSources(t, source, "marked")
			if tc.workerStage == "" {
				startRun()
			} else {
				close(releaseWorker)
			}
			awaitMarkEvent(t, ctx, runDone)

			wantStatus, wantItem, wantMessage := "completed", "skipped", "skip_processed"
			wantDone, wantSkipped, wantRunFailed, wantCalls := 0, 1, 0, int32(0)
			wantSource, wantName := "marked", file.Name
			if tc.createBefore {
				wantItem, wantMessage = "done", ""
				wantDone, wantSkipped, wantCalls = 1, 0, 1
				wantSource, wantName = "automatic", "renamed.mp4"
			} else if tc.workerStage == "analyzing" {
				wantStatus, wantItem, wantMessage = "failed", "failed", "already_processed"
				wantSkipped, wantRunFailed, wantCalls = 0, 1, 1
			}
			got := readRun(t, m, run)
			if got.Status != wantStatus || got.Discovered != 1 || got.Done != wantDone || got.Skipped != wantSkipped || got.Failed != wantRunFailed || modelCalls.Load() != wantCalls {
				t.Fatalf("got status=%s discovered=%d done=%d skipped=%d failed=%d calls=%d, want status=%s discovered=1 done=%d skipped=%d failed=%d calls=%d", got.Status, got.Discovered, got.Done, got.Skipped, got.Failed, modelCalls.Load(), wantStatus, wantDone, wantSkipped, wantRunFailed, wantCalls)
			}
			fixture.assertRunItem(t, markRunItemExpectation{run: run, status: wantItem, message: wantMessage})
			fixture.assertSources(t, wantSource, "marked")
			fixture.assertFileOutcome(t, wantName, wantDone)
		})
	}
}

type markConcurrencyFixture struct {
	manager     *Manager
	file, guard library.Entry
}

func newMarkConcurrencyFixture(t *testing.T) markConcurrencyFixture {
	t.Helper()
	m, root := setup(t)
	if err := os.Mkdir(filepath.Join(root, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"target/original.mp4", "guard.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(root, "target/original.mp4"), filepath.Join(root, "alias.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := m.Library.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Library.Refresh(t.Context(), markEntryNamed(t, m, "target").ID); err != nil {
		t.Fatal(err)
	}
	return markConcurrencyFixture{manager: m, file: markEntryNamed(t, m, "original.mp4"), guard: markEntryNamed(t, m, "guard.mp4")}
}

func markEntryNamed(t *testing.T, m *Manager, name string) library.Entry {
	t.Helper()
	var id int64
	if err := m.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM entries WHERE name=? AND present=1", name).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return readEntry(t, m, id)
}

func awaitMarkEvent[T any](t *testing.T, ctx context.Context, event <-chan T) T {
	t.Helper()
	select {
	case value := <-event:
		return value
	case <-ctx.Done():
		t.Fatalf("waiting for interleaving event: %v", ctx.Err())
		var zero T
		return zero
	}
}

func (f markConcurrencyFixture) assertSources(t *testing.T, fileSource, guardSource string) {
	t.Helper()
	for _, check := range []struct {
		entry  library.Entry
		source string
	}{{f.file, fileSource}, {f.guard, guardSource}} {
		got := readEntry(t, f.manager, check.entry.ID)
		if got.Processed != check.source {
			t.Fatalf("entry %d got processed=%q, want %q", got.ID, got.Processed, check.source)
		}
	}
}

type markRunItemExpectation struct {
	run             int64
	status, message string
}

func (f markConcurrencyFixture) assertRunItem(t *testing.T, want markRunItemExpectation) {
	t.Helper()
	var count int
	var status string
	var message domain.Record
	var finished int64
	if err := f.manager.db.View(t.Context(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM run_items WHERE run=?", want.run).Scan(&count); err != nil {
			return err
		}
		return tx.QueryRowContext(t.Context(), "SELECT status,message,finished_ms FROM run_items WHERE run=?", want.run).Scan(&status, &message, &finished)
	}); err != nil {
		t.Fatal(err)
	}
	wantFinished := want.status == "done" || want.status == "skipped" || want.status == "failed"
	if count != 1 || status != want.status || message.Code != want.message || (finished != 0) != wantFinished {
		t.Fatalf("got items=%d status=%q message=%+v finished=%d, want one %s item with message=%q finished=%v", count, status, message, finished, want.status, want.message, wantFinished)
	}
}

func (f markConcurrencyFixture) assertFileOutcome(t *testing.T, wantName string, wantOperations int) {
	t.Helper()
	m, file := f.manager, f.file
	got := readEntry(t, m, file.ID)
	if got.Name != wantName || got.Identity != file.Identity || got.Size != file.Size || got.ModifiedNS != file.ModifiedNS {
		t.Fatalf("got file %+v, want unchanged fingerprint and name=%q", got, wantName)
	}
	path := filepath.Join(m.Library.Root, "target", wantName)
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "fixture video" {
		t.Fatalf("got file content %q, err=%v, want fixture video", content, err)
	}
	other := "renamed.mp4"
	if wantName == other {
		other = file.Name
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(path), other)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got alternate path error %v, want not exist", err)
	}
	var marks, operations int
	if err := m.db.View(t.Context(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM marks").Scan(&marks); err != nil {
			return err
		}
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM operations").Scan(&operations)
	}); err != nil {
		t.Fatal(err)
	}
	if marks != 2 || operations != wantOperations {
		t.Fatalf("got marks=%d operations=%d, want marks=2 operations=%d", marks, operations, wantOperations)
	}
}
