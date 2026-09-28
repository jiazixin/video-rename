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

	"video-rename/internal/jobs"
	"video-rename/internal/models"
	"video-rename/internal/storage"

	"github.com/robfig/cron/v3"
)

type Plan struct {
	Task          string   `json:"task"`
	ModelConfigID int64    `json:"model_config_id"`
	TargetNames   []string `json:"target_names,omitempty"`
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Targets       []int64  `json:"targets"`
	Recursive     bool     `json:"recursive"`
	Kind          string   `json:"kind"`
	Expression    string   `json:"expression"`
	Timezone      string   `json:"timezone"`
	Enabled       bool     `json:"enabled"`
	Next          string   `json:"next"`
	Last          string   `json:"last"`
	Version       int64    `json:"version"`
}
type Scheduler struct {
	mu   sync.Mutex
	DB   *storage.Store
	Jobs *jobs.Manager
}

func Rule(expression, zone string) (cron.Schedule, error) {
	if len(strings.Fields(expression)) != 5 {
		return nil, fmt.Errorf("Cron 必须为五段：分 时 日 月 周")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("时区无效")
	}
	p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	s, err := p.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("Cron 表达式无效: %w", err)
	}
	spec := s.(*cron.SpecSchedule)
	spec.Location = loc
	return spec, nil
}
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
			return nil, fmt.Errorf("该规则没有可执行时间")
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
func (s *Scheduler) Get(id int64) (Plan, error) {
	p, err := scanPlan(s.DB.QueryRow("SELECT "+columns+" FROM schedules WHERE id=?", id))
	if err == nil {
		for _, target := range p.Targets {
			p.TargetNames = append(p.TargetNames, s.Jobs.Library.DisplayPath(target))
		}
	}
	return p, err
}
func (s *Scheduler) Save(p Plan) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 80 || len(p.Targets) == 0 || len(p.Targets) > 100 {
		return p, fmt.Errorf("请填写名称并选择目录")
	}
	if p.Task == "" {
		p.Task = "rename"
	}
	if p.Task != "rename" && p.Task != "extract" {
		return p, fmt.Errorf("任务类型无效")
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
			return p, fmt.Errorf("请选择有效的模型配置: %w", err)
		} else if err = profile.Validate(); err != nil {
			return p, fmt.Errorf("请完善模型配置: %w", err)
		}
	}
	switch p.Kind {
	case "hourly", "daily", "weekly", "cron":
	default:
		return p, fmt.Errorf("计划类型无效")
	}
	for _, id := range p.Targets {
		e, err := s.Jobs.Library.Get(id)
		if err != nil || e.Kind != "directory" {
			return p, fmt.Errorf("目标必须为现有目录")
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
			return p, fmt.Errorf("模型配置已删除，请重新选择")
		}
	}
	if p.ID == 0 {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM schedules").Scan(&n); err != nil {
			return p, err
		}
		if n >= 100 {
			return p, fmt.Errorf("最多支持 100 条计划")
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
			return p, fmt.Errorf("计划已被修改，请刷新")
		}
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return s.Get(p.ID)
}
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
func (s *Scheduler) Tick(now time.Time) error {
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
		if e = s.DB.QueryRow("SELECT count(*) FROM runs WHERE plan=? AND status IN ('queued','scanning','running','paused','cancelling')", p.ID).Scan(&active); e != nil {
			return e
		}
		message := "已触发"
		if active > 0 {
			message = "前次任务未结束，本次跳过"
		} else {
			_, e = s.Jobs.Create(jobs.Request{Task: p.Task, ModelConfigID: p.ModelConfigID, Targets: p.Targets, Recursive: p.Recursive, Key: fmt.Sprintf("plan:%d:%s", p.ID, wall)}, "schedule", p.ID)
			if e != nil {
				message = e.Error()
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
func (s *Scheduler) Loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := s.Tick(now); err != nil {
				_ = s.Jobs.Library.Event("failure", "scheduler", 0, 0, 0, "", "", err.Error())
			}
		}
	}
}
