package job

import (
	"context"
	"database/sql"
	"net/url"
	"strconv"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

// RunPage is a page of batches with the model profiles they use.
type RunPage struct {
	Profiles []RunProfile `json:"profiles"`
	Items    []Run        `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	Size     int          `json:"size"`
}

// RunProfile names a model profile used by a batch.
type RunProfile struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// ListPageOptions groups the inputs for ListPage.
type ListPageOptions struct {
	Category string
	Task     string
	Page     int
	Size     int
	Filters  []url.Values
}

// ListPage counts and reads matching runs in a single database snapshot.
func (m *Manager) ListPage(ctx context.Context, params ListPageOptions) (RunPage, error) {
	category, task, page, size, filters := params.Category, params.Task, params.Page, params.Size, params.Filters

	return storage.ReadValue(ctx, m.db, func(tx *sql.Tx) (RunPage, error) {
		out := RunPage{Items: []Run{}, Page: max(page, 1), Size: size}
		if size != 50 && size != 100 && size != 200 {
			out.Size = 50
		}
		var where string
		switch category {
		case "all":
			where = "1=1"
		case "manual":
			where = "r.source<>'schedule' AND r.plan=0"
		case "automatic":
			where = "(r.source='schedule' OR r.plan>0)"
		default:
			return out, domain.InvalidField("category", "invalid_choice")
		}
		var args []any
		switch task {
		case "":
		case "rename", "extract":
			where += " AND r.task=?"
			args = append(args, task)
		default:
			return out, domain.InvalidField("task", "invalid_choice")
		}
		if len(filters) > 0 {
			q := filters[0]
			if status := q.Get("status"); status != "" {
				switch status {
				case "queued", "scanning", "running", "paused", "cancelling", "completed", "partial_failure", "failed", "cancelled", "interrupted":
					where += " AND r.status=?"
					args = append(args, status)
				default:
					return out, domain.InvalidField("status", "invalid_choice")
				}
			}
			if profile := q.Get("profile"); profile != "" {
				id, err := strconv.ParseInt(profile, 10, 64)
				if err != nil || id < 0 {
					return out, domain.InvalidField("profile", "invalid_number")
				}
				where += " AND r.model_config_id=?"
				args = append(args, id)
			}
			var bounds [2]time.Time
			for i, key := range []string{"from", "to"} {
				if value := q.Get(key); value != "" {
					stamp, err := time.Parse(time.RFC3339, value)
					if err != nil {
						return out, domain.InvalidField(key, "invalid_time")
					}
					bounds[i] = stamp
					op := ">="
					if key == "to" {
						op = "<"
					}
					where += " AND julianday(COALESCE(NULLIF(r.started,''),r.created))" + op + "julianday(?)"
					args = append(args, timefmt.Format(stamp))
				}
			}
			if !bounds[0].IsZero() && !bounds[1].IsZero() && !bounds[0].Before(bounds[1]) {
				return out, domain.InvalidField("to", "not_after_from")
			}
			if duration := q.Get("duration"); duration != "" {
				ranges := map[string][2]int{"under1": {0, 60}, "1to5": {60, 300}, "5to30": {300, 1800}, "30to60": {1800, 3600}, "over60": {3600, 0}}
				span, ok := ranges[duration]
				if !ok {
					return out, domain.InvalidField("duration", "invalid_choice")
				}
				expr := "MAX(0,unixepoch(CASE WHEN r.finished<>'' THEN r.finished WHEN r.status IN " + domain.ActiveRunStatusesSQL + " THEN ? ELSE NULL END,'subsec')-unixepoch(r.started,'subsec'))"
				where += " AND r.started<>'' AND " + expr + ">=?"
				stamp := timefmt.Format(time.Now())
				args = append(args, stamp, span[0])
				if span[1] > 0 {
					where += " AND " + expr + "<?"
					args = append(args, stamp, span[1])
				}
			}
		}
		var err error
		out.Profiles = []RunProfile{}
		profiles, err := tx.QueryContext(ctx, "SELECT model_config_id,model_config_title FROM runs WHERE id IN (SELECT MAX(id) FROM runs WHERE model_config_id>0 GROUP BY model_config_id) ORDER BY model_config_id")
		if err != nil {
			return out, err
		}
		out.Profiles, err = storage.CollectRows(profiles, func(rows *sql.Rows) (RunProfile, error) {
			var profile RunProfile
			err := rows.Scan(&profile.ID, &profile.Title)
			return profile, err
		})
		if err != nil {
			return out, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM runs r WHERE "+where, args...).Scan(&out.Total); err != nil {
			return out, err
		}
		out.Page = min(out.Page, max(1, (out.Total+out.Size-1)/out.Size))
		args = append(args, out.Size, (out.Page-1)*out.Size)
		rows, err := tx.QueryContext(ctx, "SELECT "+runColumns+" FROM runs r LEFT JOIN run_token_usage u ON u.run=r.id WHERE "+where+" ORDER BY r.id DESC LIMIT ? OFFSET ?", args...)
		if err != nil {
			return out, err
		}
		out.Items, err = storage.CollectRows(rows, func(rows *sql.Rows) (Run, error) { return scanRun(rows) })
		return out, err
	})
}
