package record

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

// Success records use the attempt that produced the title; failures use the
// last request. Match both batch and entry, and leave non-model actions alone.
// Seek by entry explicitly: otherwise SQLite can scan the whole batch through
// UNIQUE(run,identity) once for every event when statistics are absent.
const historyAttemptJoin = ` LEFT JOIN model_attempts a ON (a.item,a.attempt)=(
	SELECT ma.item,ma.attempt FROM run_items i INDEXED BY item_entry JOIN model_attempts ma ON ma.item=i.id
	WHERE i.run=e.run AND i.entry=e.entry AND e.action='automatic'
	AND (e.category='failure' OR ma.status='success')
	ORDER BY i.id DESC,ma.attempt DESC LIMIT 1)`

// Reuse the existing UNIQUE(item,attempt) index for each entry's token totals,
// rather than scanning all token records in the batch for every displayed row.
const historyTokenFrom = ` FROM run_items i INDEXED BY item_entry
	JOIN token_usage_records t INDEXED BY sqlite_autoindex_token_usage_records_1 ON t.item=i.id
	WHERE i.entry=e.entry AND t.run=e.run`

// History returns a filtered page of rename outcomes.
func (s *Store) History(ctx context.Context, q Query) (any, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (any, error) {
		category := q.Get("category")
		if category != "success" && category != "failure" {
			return nil, domain.InvalidField("category", "invalid_choice")
		}
		modelSource := q.Get("model_source")
		if modelSource != "" && modelSource != "primary" && modelSource != "fallback" && modelSource != "unknown" {
			return nil, domain.InvalidField("model_source", "invalid_choice")
		}
		for _, key := range []string{"from", "to"} {
			if value := q.Get(key); value != "" {
				stamp, err := time.Parse(time.RFC3339Nano, value)
				if err != nil {
					stamp, err = time.Parse(time.DateOnly, value)
				}
				if err != nil {
					return nil, domain.InvalidField(key, "invalid_date")
				}
				q.Set(key, timefmt.Format(stamp))
			}
		}
		run := int64(0)
		if q.Get("run") != "" {
			var err error
			run, err = strconv.ParseInt(q.Get("run"), 10, 64)
			if err != nil || run <= 0 {
				return nil, domain.InvalidField("run", "invalid_number")
			}
		}
		before := number(q.Get("before"))
		if before <= 0 {
			before = 1 << 62
		}
		limit := number(q.Get("limit"))
		if limit != 50 && limit != 100 && limit != 200 {
			limit = 100
		}
		from := " FROM events e LEFT JOIN runs r ON r.id=e.run"
		where := " WHERE e.category=?"
		args := []any{category}
		if run > 0 {
			where += " AND e.run=?"
			args = append(args, run)
		}
		if search := q.Get("q"); search != "" {
			where += " AND instr(lower(e.old_path||e.new_path||e.message),lower(?))>0"
			args = append(args, search)
		}
		if action := q.Get("action"); action != "" {
			where += " AND e.action=?"
			args = append(args, action)
		}
		for _, bound := range []struct{ key, op string }{{"from", ">="}, {"to", "<="}} {
			if stamp := q.Get(bound.key); stamp != "" {
				where += " AND julianday(e.created)" + bound.op + "julianday(?)"
				args = append(args, stamp)
			}
		}
		countFrom := from
		if modelSource != "" {
			countFrom += historyAttemptJoin
			where += " AND e.action='automatic'"
			if modelSource == "unknown" {
				where += " AND a.item IS NULL"
			} else {
				where += " AND a.fallback=?"
				args = append(args, modelSource == "fallback")
			}
		}
		var err error
		var total int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+countFrom+where, args...).Scan(&total); err != nil {
			return nil, err
		}
		page, offset := int64(1), int64(0)
		if q.Has("page") {
			page = min(max(1, number(q.Get("page"))), max(1, (total+limit-1)/limit))
			offset = (page - 1) * limit
			before = 1 << 62
		}
		rows, err := tx.QueryContext(ctx, "SELECT e.id,e.category,e.action,e.entry,e.operation,e.run,e.old_path,e.new_path,e.message,e.created,COALESCE(r.config_summary,''),COALESCE(a.model_config_title,r.model_config_title,''),COALESCE(a.model,''),a.fallback,COALESCE((SELECT SUM(t.tokens)"+historyTokenFrom+"),0),COALESCE((SELECT MAX(t.estimated_records)"+historyTokenFrom+"),0)"+from+historyAttemptJoin+where+" AND e.id<? ORDER BY e.id DESC LIMIT ? OFFSET ?", append(args, before, limit+1, offset)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, entry, op, run int64
			var cat, action, old, new, created, summary, modelTitle, attemptModel string
			var msg domain.Record
			var fallback sql.NullBool
			var tokens, tokenEstimated int64
			if err = rows.Scan(&id, &cat, &action, &entry, &op, &run, &old, &new, &msg, &created, &summary, &modelTitle, &attemptModel, &fallback, &tokens, &tokenEstimated); err != nil {
				return nil, err
			}
			var snapshot struct {
				Model string `json:"model"`
			}
			if summary != "" {
				if err := json.Unmarshal([]byte(summary), &snapshot); err != nil {
					return nil, fmt.Errorf("decode run summary: %w", err)
				}
			}
			modelSource := ""
			if action == "automatic" {
				modelSource = "unknown"
				if fallback.Valid {
					snapshot.Model = attemptModel
					modelSource = "primary"
					if fallback.Bool {
						modelSource = "fallback"
					}
				}
			}
			out = append(out, map[string]any{"id": id, "category": cat, "action": action, "entry": entry, "operation": op, "run": run, "old_path": old, "new_path": new, "error": msg, "created": created, "model": snapshot.Model, "model_config_title": modelTitle, "model_source": modelSource, "tokens": tokens, "token_estimated": tokenEstimated > 0})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		next := int64(0)
		if int64(len(out)) > limit {
			out = out[:limit]
			next = out[len(out)-1]["id"].(int64)
		}
		rows.Close()
		if q.Get("paged") == "1" || q.Has("page") {
			return map[string]any{"items": out, "total": total, "next": next, "page": page}, nil
		}
		return out, nil
	})
}
