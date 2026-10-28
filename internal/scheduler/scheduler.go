// Package scheduler persists calendar rules and deduplicates scheduled triggers.
package scheduler

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/robfig/cron/v3"

	"video-rename/internal/domain"
	"video-rename/internal/job"
	"video-rename/internal/library"
	"video-rename/internal/model"
	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

// Plan is a scheduled batch rule.
type Plan struct {
	Task          string        `json:"task"`
	ModelConfigID int64         `json:"model_config_id"`
	TargetNames   []string      `json:"target_names,omitzero"`
	ID            int64         `json:"id"`
	Name          string        `json:"name"`
	Targets       []int64       `json:"targets"`
	Recursive     bool          `json:"recursive"`
	Kind          string        `json:"kind"`
	Expression    string        `json:"expression"`
	Timezone      string        `json:"timezone"`
	Enabled       bool          `json:"enabled"`
	Next          string        `json:"next"`
	Last          domain.Record `json:"last,omitzero"`
	Version       int64         `json:"version"`
}

// Scheduler persists calendar rules and starts their batches. mu serializes
// plan mutations and trigger deduplication.
type Scheduler struct {
	mu   sync.Mutex
	db   *storage.Store
	Jobs *job.Manager
}

// Rule parses a five-field cron expression in time zone zone.
func Rule(expression, zone string) (cron.Schedule, error) {
	if len(strings.Fields(expression)) != 5 {
		return nil, domain.InvalidField("expression", "cron_fields")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, domain.InvalidField("timezone", "invalid_timezone")
	}
	p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	s, err := p.Parse(expression)
	if err != nil {
		return nil, domain.InvalidField("expression", "invalid_cron").Wrap(err)
	}
	spec := s.(*cron.SpecSchedule)
	spec.Location = loc
	return spec, nil
}

// Preview returns the next run times of expression after now.
func Preview(expression, zone string, now time.Time) ([]string, error) {
	rule, err := Rule(expression, zone)
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("load schedule timezone %q: %w", zone, err)
	}
	for len(out) < 5 {
		now = rule.Next(now)
		if now.IsZero() {
			return nil, domain.InvalidField("expression", "never_runs")
		}
		key := now.In(loc).Format("2006-01-02 15:04")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, timefmt.Format(now))
	}
	return out, nil
}
func scanPlan(row interface{ Scan(...any) error }) (p Plan, err error) {
	var raw string
	err = row.Scan(&p.ID, &p.Name, &raw, &p.Recursive, &p.Kind, &p.Expression, &p.Timezone, &p.Enabled, &p.Next, &p.Last, &p.Version, &p.ModelConfigID, &p.Task)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &p.Targets)
	}
	return
}

const columns = "id,name,targets,recursive,kind,expression,timezone,enabled,next,last,version,model_config_id,task"

// List returns all plans.
func (s *Scheduler) List(ctx context.Context) ([]Plan, error) { return s.list(ctx, true) }
func (s *Scheduler) list(ctx context.Context, withNames bool) ([]Plan, error) {
	out, err := storage.ReadValue(ctx, s.db, func(tx *sql.Tx) ([]Plan, error) {
		rows, err := tx.QueryContext(ctx, "SELECT "+columns+" FROM schedules ORDER BY id DESC")
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, func(rows *sql.Rows) (Plan, error) { return scanPlan(rows) })
	})
	if err != nil {
		return nil, err
	}
	if withNames {
		for i := range out {
			for _, id := range out[i].Targets {
				out[i].TargetNames = append(out[i].TargetNames, s.Jobs.Library.DisplayPath(ctx, id))
			}
		}
	}
	return out, nil
}

// Get returns plan id.
func (s *Scheduler) Get(ctx context.Context, id int64) (Plan, error) {
	p, err := storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (Plan, error) {
		return scanPlan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM schedules WHERE id=?", id))
	})
	if err == nil {
		for _, target := range p.Targets {
			p.TargetNames = append(p.TargetNames, s.Jobs.Library.DisplayPath(ctx, target))
		}
	}
	return p, err
}

// Save creates or updates plan p.
func (s *Scheduler) Save(ctx context.Context, p Plan) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 80 || len(p.Targets) == 0 || len(p.Targets) > 100 {
		return p, domain.Invalid(domain.FieldError{Path: "name", Reason: "required"}, domain.FieldError{Path: "targets", Reason: "selection_required"})
	}
	if p.Task == "" {
		p.Task = "rename"
	}
	if p.Task != "rename" && p.Task != "extract" {
		return p, domain.InvalidField("task", "invalid_choice")
	}
	if p.Task == "extract" {
		p.ModelConfigID = 0
	} else {
		if p.ModelConfigID == 0 {
			var err error
			p.ModelConfigID, err = (model.New(s.db)).DefaultID(ctx)
			if err != nil {
				return p, err
			}
		}
		if profile, err := (model.New(s.db)).Get(ctx, p.ModelConfigID); err != nil {
			return p, domain.InvalidField("model_config_id", "invalid").Wrap(err)
		} else if err = profile.Validate(); err != nil {
			return p, domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonModelConfigIncomplete, Message: "model configuration is incomplete"}).Wrap(err)
		}
	}
	switch p.Kind {
	case "hourly", "daily", "weekly", "cron":
	default:
		return p, domain.InvalidField("kind", "invalid_choice")
	}
	for _, id := range p.Targets {
		e, err := s.Jobs.Library.Get(ctx, id)
		if err != nil || e.Kind != "directory" {
			return p, domain.InvalidField("targets", "not_directory")
		}
		if _, err = s.Jobs.Library.Path(ctx, id); err != nil {
			return p, err
		}
	}
	next, err := Preview(p.Expression, p.Timezone, time.Now())
	if err != nil {
		return p, err
	}
	p.Next = next[0]
	raw, err := json.Marshal(p.Targets)
	if err != nil {
		return p, fmt.Errorf("encode schedule targets: %w", err)
	}
	p, err = storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (Plan, error) {
		if p.Task == "rename" {
			var exists bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM model_configs WHERE id=?)", p.ModelConfigID).Scan(&exists); err != nil {
				return p, err
			}
			if !exists {
				return p, domain.Fail(domain.KindNotFound, domain.Failure{Reason: domain.ReasonModelConfigNotFound, Message: "model configuration does not exist"})
			}
		}
		if p.ID == 0 {
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM schedules").Scan(&n); err != nil {
				return p, err
			}
			if n >= 100 {
				return p, domain.Fail(domain.KindTooMany, domain.Failure{Reason: domain.ReasonSchedulesFull, Message: "at most 100 schedules"})
			}
			res, err := tx.ExecContext(ctx, "INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next,model_config_id,task) VALUES(?,?,?,?,?,?,?,?,?,?)", p.Name, string(raw), p.Recursive, p.Kind, p.Expression, p.Timezone, p.Enabled, p.Next, p.ModelConfigID, p.Task)
			if err != nil {
				return p, err
			}
			p.ID, err = res.LastInsertId()
			if err != nil {
				return p, err
			}
		} else {
			res, err := tx.ExecContext(ctx, "UPDATE schedules SET name=?,targets=?,recursive=?,kind=?,expression=?,timezone=?,enabled=?,next=?,model_config_id=?,task=?,version=version+1 WHERE id=? AND version=?", p.Name, string(raw), p.Recursive, p.Kind, p.Expression, p.Timezone, p.Enabled, p.Next, p.ModelConfigID, p.Task, p.ID, p.Version)
			if err != nil {
				return p, err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return p, err
			}
			if n == 0 {
				return p, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonScheduleChanged, Message: "schedule was modified; refresh"})
			}
		}
		return p, nil
	})
	if err != nil {
		return p, err
	}
	return s.Get(ctx, p.ID)
}

// Delete deletes plan id.
func (s *Scheduler) Delete(ctx context.Context, id, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "DELETE FROM schedules WHERE id=? AND version=?", id, version)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonScheduleChanged, Message: "schedule was modified; refresh"})
		}
		return nil
	})
}

// ResetNext deliberately does not catch up missed runs on service restart.
func (s *Scheduler) ResetNext(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plans, err := s.list(ctx, false)
	if err != nil {
		return err
	}
	for _, p := range plans {
		times, err := Preview(p.Expression, p.Timezone, now)
		if err != nil {
			return err
		}
		if _, err = storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "UPDATE schedules SET next=? WHERE id=?", times[0], p.ID)
		}); err != nil {
			return err
		}
	}
	return nil
}

// Tick starts the enabled plans that are due at now and advances their next run
// time.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plans, err := s.list(ctx, false)
	if err != nil {
		return err
	}
	for _, p := range plans {
		if !p.Enabled {
			continue
		}
		at, err := time.Parse(timefmt.Layout, p.Next)
		if err != nil {
			return err
		}
		if at.After(now) {
			continue
		}
		next, err := Preview(p.Expression, p.Timezone, now)
		if err != nil {
			return err
		}
		loc, err := time.LoadLocation(p.Timezone)
		if err != nil {
			return fmt.Errorf("load schedule timezone %q: %w", p.Timezone, err)
		}
		wall := at.In(loc).Format("2006-01-02 15:04")
		n, err := storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (int64, error) {
			result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schedule_ticks VALUES(?,?,?)", p.ID, wall, storage.Now())
			if err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE schedules SET next=? WHERE id=?", next[0], p.ID); err != nil {
				return 0, err
			}
			return result.RowsAffected()
		})
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		var active int
		if err = s.db.View(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE plan=? AND status IN "+domain.ActiveRunStatusesSQL, p.ID).Scan(&active)
		}); err != nil {
			return err
		}
		message := domain.Record{Code: "schedule_triggered", Message: "triggered"}.Text()
		if active > 0 {
			message = domain.Record{Code: "schedule_skipped_active", Message: "previous run unfinished; skipped"}.Text()
		} else {
			_, err = s.Jobs.Create(ctx, job.Request{Task: p.Task, ModelConfigID: p.ModelConfigID, Targets: p.Targets, Recursive: p.Recursive, Key: fmt.Sprintf("plan:%d:%s", p.ID, wall)}, "schedule", p.ID)
			if err != nil {
				message = domain.RecordText(err)
				if err := s.Jobs.Library.Event(ctx, library.EventInput{Category: "failure", Action: "schedule", Entry: 0, Operation: 0, Run: 0, Old: "", New: "", Message: message}); err != nil {
					return err
				}
			}
		}
		if _, err = storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "UPDATE schedules SET last=? WHERE id=?", message, p.ID)
		}); err != nil {
			return err
		}
	}
	return nil
}

// Run calls Tick every second until ctx ends.
func (s *Scheduler) Run(ctx context.Context) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if err := s.Tick(ctx, now); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if recordErr := s.Jobs.Library.Event(ctx, library.EventInput{Category: "failure", Action: "scheduler", Message: domain.RecordText(err)}); recordErr != nil {
					return errors.Join(err, fmt.Errorf("record scheduler failure: %w", recordErr))
				}
			}
		}
	}
}

// New creates the schedule store and its batch runner.
func New(db *storage.Store, manager *job.Manager) *Scheduler {
	return &Scheduler{db: db, Jobs: manager}
}
