package jobs

import (
	"context"
	"fmt"
)

type RunPage struct {
	Items []Run `json:"items"`
	Total int   `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

// ListPage counts and reads matching runs in a single database snapshot.
func (m *Manager) ListPage(ctx context.Context, category, task string, page, size int) (RunPage, error) {
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
		return out, fmt.Errorf("运行分类无效")
	}
	var args []any
	switch task {
	case "":
	case "rename", "extract":
		where += " AND r.task=?"
		args = append(args, task)
	default:
		return out, fmt.Errorf("任务类型无效")
	}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM runs r WHERE "+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	out.Page = min(out.Page, max(1, (out.Total+out.Size-1)/out.Size))
	args = append(args, out.Size, (out.Page-1)*out.Size)
	rows, err := tx.QueryContext(ctx, "SELECT "+runColumns+" FROM runs r LEFT JOIN run_token_usage u ON u.run=r.id WHERE "+where+" ORDER BY r.id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, run)
	}
	return out, rows.Err()
}
