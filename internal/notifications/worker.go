package notifications

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// Delivery is one notification delivery.
type Delivery struct {
	ID       int64         `json:"id"`
	Kind     string        `json:"kind"`
	Run      int64         `json:"run"`
	Status   string        `json:"status"`
	Attempts int           `json:"attempts"`
	Error    domain.Record `json:"error,omitzero"`
	Updated  string        `json:"updated"`
}

// History returns the 30 latest deliveries.
func (s *Service) History(ctx context.Context) ([]Delivery, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,kind,run,status,attempts,error,updated FROM notification_deliveries ORDER BY id DESC LIMIT 30")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err = rows.Scan(&d.ID, &d.Kind, &d.Run, &d.Status, &d.Attempts, &d.Error, &d.Updated); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Run delivers pending notifications until ctx ends.
func (s *Service) Run(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, "UPDATE notification_deliveries SET status=CASE WHEN attempts>=3 THEN 'failed' ELSE 'pending' END,error=? WHERE status='sending'", domain.Record{Code: "notification_unconfirmed", Message: "service restarted; the last delivery is unconfirmed"}.Text()); err != nil {
		s.logger.Warn("notification_recovery_failed", "error", err)
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if ctx.Err() == nil {
				if err := s.tick(ctx); err != nil {
					s.logger.Warn("notification_worker_failed", "error", err)
				}
			}
		}
	}
}
func clip(text string, n int) string {
	r := []rune(text)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}
func (s *Service) tick(ctx context.Context) error {
	s.mu.Lock()
	err := s.enqueue(ctx)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	var d Delivery
	var title, body string
	err = s.DB.QueryRowContext(ctx, `SELECT d.id,d.kind,d.run,d.title,d.body,d.attempts FROM notification_deliveries d JOIN notification_channels c ON c.kind=d.kind WHERE d.status='pending' AND c.enabled=1 AND d.next_attempt<=? ORDER BY d.id LIMIT 1`, storage.Now()).Scan(&d.ID, &d.Kind, &d.Run, &title, &body, &d.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	c, credentials, err := s.get(ctx, d.Kind)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return nil
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE notification_deliveries SET status='sending',attempts=attempts+1 WHERE id=? AND status='pending'", d.ID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil
	}
	err = s.Send(ctx, credentials, d.Kind, title, body)
	status, message := "sent", ""
	if err != nil {
		status = "pending"
		message = domain.RecordText(err)
		if d.Attempts+1 >= 3 {
			status = "failed"
		}
	}
	// The attempt happened; record it even if shutdown cancelled the send.
	record, cancel := storage.Detach(ctx)
	defer cancel()
	_, writeErr := s.DB.ExecContext(record, "UPDATE notification_deliveries SET status=?,error=?,updated=?,next_attempt=? WHERE id=? AND status='sending'", status, message, storage.Now(), time.Now().UTC().Add(time.Duration(d.Attempts+1)*30*time.Second).Format(time.RFC3339Nano), d.ID)
	return writeErr
}
func (s *Service) enqueue(ctx context.Context) error {
	var timezone string
	if err := s.DB.QueryRowContext(ctx, "SELECT timezone FROM notification_preferences WHERE id=1").Scan(&timezone); err != nil {
		return err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.kind,r.id,r.source,r.status,r.targets,r.created,r.started,r.finished,r.discovered,r.done,r.skipped,r.failed,r.model_config_title,r.plan FROM notification_channels c JOIN runs r ON r.id>c.after_run WHERE c.enabled=1 AND r.status IN ('completed','partial_failure','failed','cancelled','interrupted') AND (c.failure_only=0 OR r.failed>0 OR r.status IN ('partial_failure','failed','interrupted')) AND NOT EXISTS(SELECT 1 FROM notification_deliveries d WHERE d.kind=c.kind AND d.run=r.id) ORDER BY r.id LIMIT 100`)
	if err != nil {
		return err
	}
	type batch struct {
		kind, source, status, targets, created, started, finished, model string
		id, plan                                                         int64
		total, done, skipped, failed                                     int
	}
	batches := []batch{}
	for rows.Next() {
		var b batch
		if err = rows.Scan(&b.kind, &b.id, &b.source, &b.status, &b.targets, &b.created, &b.started, &b.finished, &b.total, &b.done, &b.skipped, &b.failed, &b.model, &b.plan); err != nil {
			break
		}
		batches = append(batches, b)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// Titles and bodies are message content for chat apps, a recorded
	// exception to the error-code rule (README, code exceptions).
	for _, b := range batches {
		labels := map[string]string{"completed": "批次成功", "partial_failure": "批次失败（部分失败）", "failed": "批次失败", "cancelled": "批次已取消", "interrupted": "批次失败（服务中断）"}
		source := "手动运行"
		if b.source == "schedule" {
			source = "自动计划"
		} else if b.plan > 0 {
			source = "计划立即运行"
		}
		var ids []int64
		json.Unmarshal([]byte(b.targets), &ids)
		names := []string{}
		for i, id := range ids {
			if i >= 5 {
				names = append(names, fmt.Sprintf("另 %d 个目标", len(ids)-5))
				break
			}
			var name string
			if id == 1 {
				name = "整个媒体库"
			} else if s.DB.QueryRowContext(ctx, "SELECT name FROM entries WHERE id=?", id).Scan(&name) != nil {
				name = fmt.Sprintf("条目 #%d", id)
			}
			names = append(names, clip(name, 60))
		}
		started, errStart := time.Parse(time.RFC3339Nano, b.started)
		finished, errEnd := time.Parse(time.RFC3339Nano, b.finished)
		duration := "—"
		if errStart == nil && errEnd == nil {
			duration = finished.Sub(started).Round(time.Second).String()
		}
		date := func(raw string) string {
			t, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil {
				return "未开始 / 未记录"
			}
			return t.In(location).Format("2006-01-02 15:04:05 -07:00") + " (" + timezone + ")"
		}
		left := max(b.total-b.done-b.skipped-b.failed, 0)
		title := fmt.Sprintf("video-rename · %s", labels[b.status])
		body := fmt.Sprintf("批次编号：#%d\n视频总数：%d\n成功：%d\n失败：%d\n跳过：%d\n未完成：%d\n开始时间：%s\n完成时间：%s\n处理耗时：%s\n来源：%s\n目录：%s\n模型：%s\n提交时间：%s", b.id, b.total, b.done, b.failed, b.skipped, left, date(b.started), date(b.finished), duration, source, strings.Join(names, "、"), clip(b.model, 60), date(b.created))
		_, err = s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO notification_deliveries(kind,run,title,body,created,updated,next_attempt) VALUES(?,?,?,?,?,?,?)", b.kind, b.id, title, body, storage.Now(), storage.Now(), storage.Now())
		if err != nil {
			return err
		}
	}
	return nil
}
