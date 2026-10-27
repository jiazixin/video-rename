// Package scheduler persists calendar rules and deduplicates scheduled triggers.
package scheduler

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/robfig/cron/v3"

	"video-rename/internal/domain"
	"video-rename/internal/jobs"
	"video-rename/internal/models"
	"video-rename/internal/storage"
)

// Plan is a scheduled batch rule.
type Plan struct {
	Task          string        `json:"task"`
	ModelConfigID int64         `json:"model_config_id"`
	TargetNames   []string      `json:"target_names,omitempty"`
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

// Scheduler persists calendar rules and starts their batches.
type Scheduler struct {
	mu   sync.Mutex
	DB   *storage.Store
	Jobs *jobs.Manager
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
	loc, _ := time.LoadLocation(zone)
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
		out = append(out, now.In(loc).Format(time.RFC3339))
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
func (s *Scheduler) List() ([]Plan, error) { return s.list(true) }
func (s *Scheduler) list(withNames bool) ([]Plan, error) {
	rows, err := s.DB.Query("SELECT " + columns + " FROM schedules ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		p, e := scanPlan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if withNames {
		for i := range out {
			for _, id := range out[i].Targets {
				out[i].TargetNames = append(out[i].TargetNames, s.Jobs.Library.DisplayPath(id))
			}
		}
	}
	return out, nil
}

// Get returns plan id.
func (s *Scheduler) Get(id int64) (Plan, error) {
	p, err := scanPlan(s.DB.QueryRow("SELECT "+columns+" FROM schedules WHERE id=?", id))
	if err == nil {
		for _, target := range p.Targets {
			p.TargetNames = append(p.TargetNames, s.Jobs.Library.DisplayPath(target))
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
			p.ModelConfigID, err = (models.Store{DB: s.DB}).DefaultID()
			if err != nil {
				return p, err
			}
		}
		if profile, err := (models.Store{DB: s.DB}).Get(p.ModelConfigID); err != nil {
			return p, domain.InvalidField("model_config_id", "invalid").Wrap(err)
		} else if err = profile.Validate(); err != nil {
			return p, domain.Fail(domain.KindInvalid, "model_config_incomplete", "model configuration is incomplete").Wrap(err)
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
		if _, err = s.Jobs.Library.Path(id); err != nil {
			return p, err
		}
	}
	next, err := Preview(p.Expression, p.Timezone, time.Now())
	if err != nil {
		return p, err
	}
	p.Next = next[0]
	raw, _ := json.Marshal(p.Targets)
	tx, err := s.DB.Begin()
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if p.Task == "rename" {
		var exists bool
		if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM model_configs WHERE id=?)", p.ModelConfigID).Scan(&exists); err != nil {
			return p, err
		}
		if !exists {
			return p, domain.Fail(domain.KindNotFound, "model_config_not_found", "model configuration does not exist")
		}
	}
	if p.ID == 0 {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM schedules").Scan(&n); err != nil {
			return p, err
		}
		if n >= 100 {
			return p, domain.Fail(domain.KindTooMany, "schedules_full", "at most 100 schedules")
		}
		res, err := tx.Exec("INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next,model_config_id,task) VALUES(?,?,?,?,?,?,?,?,?,?)", p.Name, string(raw), p.Recursive, p.Kind, p.Expression, p.Timezone, p.Enabled, p.Next, p.ModelConfigID, p.Task)
		if err != nil {
			return p, err
		}
		p.ID, _ = res.LastInsertId()
	} else {
		res, err := tx.Exec("UPDATE schedules SET name=?,targets=?,recursive=?,kind=?,expression=?,timezone=?,enabled=?,next=?,model_config_id=?,task=?,version=version+1 WHERE id=? AND version=?", p.Name, string(raw), p.Recursive, p.Kind, p.Expression, p.Timezone, p.Enabled, p.Next, p.ModelConfigID, p.Task, p.ID, p.Version)
		if err != nil {
			return p, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return p, domain.Fail(domain.KindConflict, "schedule_changed", "schedule was modified; refresh")
		}
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return s.Get(p.ID)
}

// Delete deletes plan id.
func (s *Scheduler) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec("DELETE FROM schedules WHERE id=?", id)
	return err
}

// ResetNext deliberately does not catch up missed runs on service restart.
func (s *Scheduler) ResetNext(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plans, err := s.list(false)
	if err != nil {
		return err
	}
	for _, p := range plans {
		times, e := Preview(p.Expression, p.Timezone, now)
		if e != nil {
			return e
		}
		if _, e = s.DB.Exec("UPDATE schedules SET next=? WHERE id=?", times[0], p.ID); e != nil {
			return e
		}
	}
	return nil
}

// Tick starts the enabled plans that are due at now and advances their next run
// time.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plans, err := s.list(false)
	if err != nil {
		return err
	}
	for _, p := range plans {
		if !p.Enabled {
			continue
		}
		at, e := time.Parse(time.RFC3339, p.Next)
		if e != nil {
			return e
		}
		if at.After(now) {
			continue
		}
		next, e := Preview(p.Expression, p.Timezone, now)
		if e != nil {
			return e
		}
		loc, _ := time.LoadLocation(p.Timezone)
		wall := at.In(loc).Format("2006-01-02 15:04")
		tx, e := s.DB.Begin()
		if e != nil {
			return e
		}
		res, e := tx.Exec("INSERT OR IGNORE INTO schedule_ticks VALUES(?,?,?)", p.ID, wall, storage.Now())
		if e != nil {
			tx.Rollback()
			return e
		}
		n, _ := res.RowsAffected()
		if _, e = tx.Exec("UPDATE schedules SET next=? WHERE id=?", next[0], p.ID); e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		if n == 0 {
			continue
		}
		var active int
		if e = s.DB.QueryRow("SELECT count(*) FROM runs WHERE plan=? AND status IN "+domain.ActiveRunStatusesSQL, p.ID).Scan(&active); e != nil {
			return e
		}
		message := domain.Record{Code: "schedule_triggered", Message: "triggered"}.Text()
		if active > 0 {
			message = domain.Record{Code: "schedule_skipped_active", Message: "previous run unfinished; skipped"}.Text()
		} else {
			_, e = s.Jobs.Create(ctx, jobs.Request{Task: p.Task, ModelConfigID: p.ModelConfigID, Targets: p.Targets, Recursive: p.Recursive, Key: fmt.Sprintf("plan:%d:%s", p.ID, wall)}, "schedule", p.ID)
			if e != nil {
				message = domain.RecordText(e)
				if er := s.Jobs.Library.Event("failure", "schedule", 0, 0, 0, "", "", message); er != nil {
					return er
				}
			}
		}
		if _, e = s.DB.Exec("UPDATE schedules SET last=? WHERE id=?", message, p.ID); e != nil {
			return e
		}
	}
	return nil
}

// Loop calls Tick every second until ctx ends.
func (s *Scheduler) Loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := s.Tick(ctx, now); err != nil {
				_ = s.Jobs.Library.Event("failure", "scheduler", 0, 0, 0, "", "", domain.RecordText(err))
			}
		}
	}
}
