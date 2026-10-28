package notification

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
	"video-rename/internal/timefmt"
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
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) ([]Delivery, error) {
		rows, err := tx.QueryContext(ctx, "SELECT id,kind,run,status,attempts,error,updated FROM notification_deliveries ORDER BY id DESC LIMIT 30")
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, scanDelivery)
	})
}

func scanDelivery(rows *sql.Rows) (Delivery, error) {
	var d Delivery
	err := rows.Scan(&d.ID, &d.Kind, &d.Run, &d.Status, &d.Attempts, &d.Error, &d.Updated)
	return d, err
}

// Run delivers pending notifications until ctx ends.
func (s *Service) Run(ctx context.Context) error {
	if _, err := storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE notification_deliveries SET status=CASE WHEN attempts>=3 THEN 'failed' ELSE 'pending' END,error=? WHERE status='sending'", domain.Record{Code: "notification_unconfirmed", Message: "service restarted; the last delivery is unconfirmed"}.Text())
	}); err != nil {
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
	err = s.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT d.id,d.kind,d.run,d.title,d.body,d.attempts FROM notification_deliveries d JOIN notification_channels c ON c.kind=d.kind WHERE d.status='pending' AND c.enabled=1 AND d.next_attempt<=? ORDER BY d.id LIMIT 1`, storage.Now()).Scan(&d.ID, &d.Kind, &d.Run, &title, &body, &d.Attempts)
	})
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
	result, err := storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "UPDATE notification_deliveries SET status='sending',attempts=attempts+1 WHERE id=? AND status='pending'", d.ID)
	})
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	err = s.send(ctx, message{credentials: credentials, kind: d.Kind, title: title, body: body})
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
	_, err = storage.WriteValue(record, s.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(record, "UPDATE notification_deliveries SET status=?,error=?,updated=?,next_attempt=? WHERE id=? AND status='sending'", status, message, storage.Now(), timefmt.Format(time.Now().Add(time.Duration(d.Attempts+1)*30*time.Second)), d.ID)
	})
	return err
}
func (s *Service) enqueue(ctx context.Context) error {
	var timezone string
	if err := s.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT timezone FROM notification_preferences WHERE id=1").Scan(&timezone)
	}); err != nil {
		return err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return err
	}
	type batch struct {
		kind, source, status, targets, created, started, finished, model string
		id, plan                                                         int64
		total, done, skipped, failed                                     int
	}
	batches := []batch{}
	err = s.db.View(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.kind,r.id,r.source,r.status,r.targets,r.created,r.started,r.finished,r.discovered,r.done,r.skipped,r.failed,r.model_config_title,r.plan FROM notification_channels c JOIN runs r ON r.id>c.after_run WHERE c.enabled=1 AND r.status IN ('completed','partial_failure','failed','cancelled','interrupted') AND (c.failure_only=0 OR r.failed>0 OR r.status IN ('partial_failure','failed','interrupted')) AND NOT EXISTS(SELECT 1 FROM notification_deliveries d WHERE d.kind=c.kind AND d.run=r.id) ORDER BY r.id LIMIT 100`)
		if err != nil {
			return err
		}
		batches, err = storage.CollectRows(rows, func(rows *sql.Rows) (batch, error) {
			var b batch
			err := rows.Scan(&b.kind, &b.id, &b.source, &b.status, &b.targets, &b.created, &b.started, &b.finished, &b.total, &b.done, &b.skipped, &b.failed, &b.model, &b.plan)
			return b, err
		})
		return err
	})
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
		if err := json.Unmarshal([]byte(b.targets), &ids); err != nil {
			return fmt.Errorf("decode notification targets for run %d: %w", b.id, err)
		}
		names := []string{}
		for i, id := range ids {
			if i >= 5 {
				names = append(names, fmt.Sprintf("另 %d 个目标", len(ids)-5))
				break
			}
			var name string
			if id == 1 {
				name = "整个媒体库"
			} else {
				err := s.db.View(ctx, func(tx *sql.Tx) error {
					return tx.QueryRowContext(ctx, "SELECT name FROM entries WHERE id=?", id).Scan(&name)
				})
				if errors.Is(err, sql.ErrNoRows) {
					// Deleted entries retain their stable ID in historical summaries.
					name = fmt.Sprintf("条目 #%d", id)
				} else if err != nil {
					return fmt.Errorf("read notification target %d: %w", id, err)
				}
			}
			names = append(names, clip(name, 60))
		}
		var created, started, finished time.Time
		for _, field := range []struct {
			name   string
			value  string
			target *time.Time
		}{{"created", b.created, &created}, {"started", b.started, &started}, {"finished", b.finished, &finished}} {
			if field.value == "" {
				continue
			}
			parsed, err := time.Parse(timefmt.Layout, field.value)
			if err != nil {
				return fmt.Errorf("parse notification run %d %s time: %w", b.id, field.name, err)
			}
			*field.target = parsed
		}
		duration := "—"
		if !started.IsZero() && !finished.IsZero() {
			duration = finished.Sub(started).Round(time.Second).String()
		}
		date := func(t time.Time) string {
			if t.IsZero() {
				return "未开始 / 未记录"
			}
			return t.In(location).Format("2006-01-02 15:04:05 -07:00") + " (" + timezone + ")"
		}
		left := max(b.total-b.done-b.skipped-b.failed, 0)
		title := fmt.Sprintf("video-rename · %s", labels[b.status])
		body := fmt.Sprintf("批次编号：#%d\n视频总数：%d\n成功：%d\n失败：%d\n跳过：%d\n未完成：%d\n开始时间：%s\n完成时间：%s\n处理耗时：%s\n来源：%s\n目录：%s\n模型：%s\n提交时间：%s", b.id, b.total, b.done, b.failed, b.skipped, left, date(started), date(finished), duration, source, strings.Join(names, "、"), clip(b.model, 60), date(created))
		_, err = storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "INSERT OR IGNORE INTO notification_deliveries(kind,run,title,body,created,updated,next_attempt) VALUES(?,?,?,?,?,?,?)", b.kind, b.id, title, body, storage.Now(), storage.Now(), storage.Now())
		})
		if err != nil {
			return err
		}
	}
	return nil
}
