package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/job"
	"video-rename/internal/library"
	"video-rename/internal/model"
	promptstore "video-rename/internal/prompt"
	"video-rename/internal/storage"
	"video-rename/internal/systemsetting"
	"video-rename/internal/timefmt"
)

// logRecorder collects log messages for assertions.
type logRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *logRecorder) WithGroup(string) slog.Handler            { return r }
func (r *logRecorder) Handle(_ context.Context, record slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, record.Message)
	return nil
}

func (r *logRecorder) count(message string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, logged := range r.messages {
		if logged == message {
			count++
		}
	}
	return count
}

// schedulerFixture is a scheduler over an empty media library with a fake
// toolchain; its batches are created but never executed.
type schedulerFixture struct {
	*Scheduler
	db   *storage.Store
	logs *logRecorder
}

func newSchedulerFixture(t *testing.T) schedulerFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "media")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", base)
	db, err := storage.Open(t.Context(), filepath.Join(base, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	l, err := library.New(t.Context(), db, root, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	m, err := job.New(t.Context(), job.Options{Store: db, Library: l, Config: &config.Config{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Cache.Close(); err != nil {
			t.Error(err)
		}
	})
	logs := &logRecorder{}
	return schedulerFixture{Scheduler: New(db, m, slog.New(logs)), db: db, logs: logs}
}

// extractPlan saves an enabled every-minute extraction plan, which needs no
// model configuration, and returns it with its first run time.
func (f schedulerFixture) extractPlan(t *testing.T, name string) (Plan, time.Time) {
	t.Helper()
	p, err := f.Save(t.Context(), Plan{Task: "extract", Name: name, Targets: []int64{1}, Kind: "cron", Expression: "* * * * *", Timezone: "UTC", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(timefmt.Layout, p.Next)
	if err != nil {
		t.Fatal(err)
	}
	return p, at
}

func (f schedulerFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := storage.WriteValue(t.Context(), f.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), query, args...)
	}); err != nil {
		t.Fatal(err)
	}
}

func (f schedulerFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), query, args...).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// The scheduler used to return from Run when a failed tick could not be
// recorded either, so no plan fired again until the service restarted. The
// failure is now observable in the service log and the loop keeps running.
func TestSchedulerSurvivesUnrecordableFailure(t *testing.T) {
	f := newSchedulerFixture(t)
	_, at := f.extractPlan(t, "survivor")
	// The trigger write and the failure record both fail, as on a full disk.
	f.exec(t, "CREATE TRIGGER reject_tick BEFORE INSERT ON schedule_ticks BEGIN SELECT RAISE(ABORT,'tick write failed'); END")
	f.exec(t, "CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'event write failed'); END")
	for range 3 {
		f.tick(t.Context(), at)
	}
	if got := f.logs.count("schedule_failed"); got != 1 {
		t.Fatalf("got %d schedule_failed log records for one persisting failure, want 1", got)
	}
	if got := f.count(t, "SELECT count(*) FROM runs"); got != 0 {
		t.Fatalf("got %d runs while the trigger write fails, want 0", got)
	}
	f.exec(t, "DROP TRIGGER reject_tick")
	f.exec(t, "DROP TRIGGER reject_event")
	f.tick(t.Context(), at)
	if got := f.count(t, "SELECT count(*) FROM runs"); got != 1 {
		t.Fatalf("got %d runs after the database recovered, want the due plan to fire once", got)
	}
}

func TestSchedulerRunStopsOnlyWithContext(t *testing.T) {
	f := newSchedulerFixture(t)
	f.exec(t, "CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'event write failed'); END")
	// A stored plan that cannot be decoded fails every tick before any plan runs.
	f.exec(t, "INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next,model_config_id,task) VALUES('broken','not json',0,'cron','* * * * *','UTC',1,'2026-01-01T00:00:00.000Z',0,'extract')")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	// Run ticks once a second; wait for the first failing tick.
	deadline := time.Now().Add(10 * time.Second)
	for f.logs.count("schedule_failed") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("scheduler did not report the failing tick")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned %v after a failed tick, want it to keep running", err)
	default:
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("got %v, want nil when the context ends", err)
	}
}

func TestSchedulerIsolatesFailingPlan(t *testing.T) {
	f := newSchedulerFixture(t)
	broken, at := f.extractPlan(t, "broken")
	healthy, _ := f.extractPlan(t, "healthy")
	// The stored time zone of one plan no longer loads.
	f.exec(t, "UPDATE schedules SET timezone='Invalid/Zone' WHERE id=?", broken.ID)
	for range 5 {
		if err := f.Tick(t.Context(), at); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.count(t, "SELECT count(*) FROM runs WHERE plan=?", healthy.ID); got != 1 {
		t.Fatalf("got %d runs of the healthy plan, want 1", got)
	}
	if got := f.count(t, "SELECT count(*) FROM events WHERE category='failure' AND action='schedule'"); got != 1 {
		t.Fatalf("got %d failure events for one persisting failure, want 1", got)
	}
	stored, err := f.Get(t.Context(), broken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Last.Code != "validation_failed" {
		t.Fatalf("got last result %+v of the failing plan, want the time zone failure", stored.Last)
	}
	// Once repaired the plan fires, and a later failure is reported again. The
	// healthy plan's batch covers the same directory and must finish first.
	f.exec(t, "UPDATE runs SET status='completed'")
	f.exec(t, "UPDATE schedules SET timezone='UTC' WHERE id=?", broken.ID)
	if err := f.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	if got := f.count(t, "SELECT count(*) FROM runs WHERE plan=?", broken.ID); got != 1 {
		t.Fatalf("got %d runs of the repaired plan, want 1", got)
	}
	f.exec(t, "UPDATE schedules SET timezone='Invalid/Zone',next=? WHERE id=?", timefmt.Format(at), broken.ID)
	if err := f.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	if got := f.count(t, "SELECT count(*) FROM events WHERE category='failure' AND action='schedule'"); got != 2 {
		t.Fatalf("got %d failure events after the failure returned, want 2", got)
	}
}

func TestSaveReportsEachInvalidField(t *testing.T) {
	f := newSchedulerFixture(t)
	valid := Plan{Task: "extract", Name: "nightly", Targets: []int64{1}, Kind: "daily", Expression: "0 0 * * *", Timezone: "UTC"}
	for _, tc := range []struct {
		name   string
		change func(*Plan)
		want   []domain.FieldError
	}{
		{"empty name", func(p *Plan) { p.Name = "  " }, []domain.FieldError{{Path: "name", Reason: "required"}}},
		{"long name", func(p *Plan) { p.Name = strings.Repeat("计", 81) }, []domain.FieldError{{Path: "name", Reason: "too_long"}}},
		{"no target", func(p *Plan) { p.Targets = nil }, []domain.FieldError{{Path: "targets", Reason: "selection_required"}}},
		{"too many targets", func(p *Plan) { p.Targets = make([]int64, 101) }, []domain.FieldError{{Path: "targets", Reason: "too_many"}}},
		{"both", func(p *Plan) { p.Name, p.Targets = "", nil }, []domain.FieldError{{Path: "name", Reason: "required"}, {Path: "targets", Reason: "selection_required"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.change(&p)
			_, err := f.Save(t.Context(), p)
			validation, ok := errors.AsType[*domain.ValidationError](err)
			if !ok || !slices.Equal(validation.Fields, tc.want) {
				t.Fatalf("got %v, want field errors %v", err, tc.want)
			}
		})
	}
}

func TestRulePreview(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct{ expr, zone, want string }{{"15 * * * *", "Asia/Shanghai", "2026-09-20T00:15:00.000Z"}, {"30 2 * * *", "Asia/Shanghai", "2026-09-20T18:30:00.000Z"}, {"0 3 * * 1", "Asia/Shanghai", "2026-09-20T19:00:00.000Z"}} {
		v, e := Preview(c.expr, c.zone, now)
		if e != nil || len(v) != 5 || v[0] != c.want {
			t.Fatalf("%+v: %v %v", c, v, e)
		}
	}
	for _, expr := range []string{"* * * * * *", "60 * * * *", "@hourly", "0 0 31 2 *"} {
		if _, err := Preview(expr, "Asia/Shanghai", now); err == nil {
			t.Fatal(expr)
		}
	}
	if _, err := Rule("* * * * *", "invalid/zone"); err == nil {
		t.Fatal("bad zone accepted")
	}
}
func TestDSTPreviewNoDuplicateWallTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	v, err := Preview("30 1 * * *", "America/New_York", now)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range v {
		instant, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		wall := instant.In(location).Format("2006-01-02 15:04")
		if seen[wall] {
			t.Fatal("duplicate DST wall time", v)
		}
		seen[wall] = true
	}
}

func TestPersistentTriggerDeduplicationAndOverlap(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Error(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Error(err)
		}
	}
	t.Setenv("PATH", base)
	db, err := storage.Open(t.Context(), filepath.Join(base, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(db.Close)
	l, err := library.New(t.Context(), db, root, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m, err := job.New(t.Context(), job.Options{Store: db, Library: l, Config: &config.Config{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer func(fn func() error) {
		if err := fn(); err != nil {
			t.Error(err)
		}
	}(m.Cache.Close)
	s := New(db, m, slog.New(slog.DiscardHandler))
	libraryPrompt, err := (promptstore.New(db)).Save(t.Context(), promptstore.Profile{Title: "计划提示词", Body: "计划专属要求"})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := (model.New(db)).Save(t.Context(), model.Profile{Provider: "custom", Protocol: "openai", BaseURL: "http://127.0.0.1:8080/v1", Model: "test-model", TimeoutMS: 2000, Temperature: 0.2, MaxTokens: 128, Title: "计划配置", PromptID: libraryPrompt.ID})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(t.Context(), Plan{ModelConfigID: prompt.ID, Name: "  " + strings.Repeat("计", 80) + "  ", Targets: []int64{1}, Recursive: true, Kind: "cron", Expression: "* * * * *", Timezone: "Asia/Shanghai", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != strings.Repeat("计", 80) {
		t.Fatalf("plan name was not trimmed: %q", p.Name)
	}
	tooLong := p
	tooLong.Name += "划"
	if _, err := s.Save(t.Context(), tooLong); err == nil {
		t.Fatal("accepted plan name longer than 80 characters")
	}
	at, err := time.Parse(time.RFC3339, p.Next)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM runs").Scan(&n) }); err != nil {
		t.Error(err)
	}
	var savedModelConfigID int64
	var savedBody string
	if err = db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT model_config_id,prompt_body FROM runs LIMIT 1").Scan(&savedModelConfigID, &savedBody)
	}); err != nil || savedModelConfigID != prompt.ID || savedBody != libraryPrompt.Body+"\n\n"+systemsetting.DefaultFixedPrompt {
		t.Fatal(savedModelConfigID, savedBody, err)
	}
	if n != 1 {
		t.Fatalf("duplicate triggers %d", n)
	}
	if err = s.Tick(t.Context(), at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM runs").Scan(&n) }); err != nil {
		t.Error(err)
	}
	if n != 1 {
		t.Fatalf("overlapping trigger %d", n)
	}
	p, err = s.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Last.Code != "schedule_skipped_active" {
		t.Fatal(p.Last)
	}
	if err = s.ResetNext(t.Context(), at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.View(t.Context(), func(tx *sql.Tx) error { return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM runs").Scan(&n) }); err != nil {
		t.Error(err)
	}
	if n != 1 {
		t.Fatal("restart caught up missed runs")
	}
	p.Enabled = false
	if _, err = s.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(t.Context(), p); err == nil {
		t.Fatal("stale edit accepted")
	}
	// Switching a persisted plan to extraction must bypass model validation,
	// survive reload, and pass the task type through the automatic trigger.
	p, err = s.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.Task, p.ModelConfigID, p.Enabled = "extract", 999999, true
	p, err = s.Save(t.Context(), p)
	if err != nil || p.Task != "extract" || p.ModelConfigID != 0 {
		t.Fatalf("extract plan: %+v %v", p, err)
	}
	if _, err = storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE runs SET status='cancelled'")
	}); err != nil {
		t.Fatal(err)
	}
	at, err = time.Parse(time.RFC3339, p.Next)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// The prior wall-clock tick can already be deduplicated; trigger the next one.
	p, err = s.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	at, err = time.Parse(time.RFC3339, p.Next)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	var task string
	if err = db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT task,model_config_id,prompt_body FROM runs ORDER BY id DESC LIMIT 1").Scan(&task, &savedModelConfigID, &savedBody)
	}); err != nil || task != "extract" || savedModelConfigID != 0 || savedBody != "" {
		t.Fatal(task, savedModelConfigID, savedBody, err)
	}
	if err = l.Refresh(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
}
