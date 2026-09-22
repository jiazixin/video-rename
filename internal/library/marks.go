package library

import (
	"context"
	"fmt"
	"video-rename/internal/discovery"
	"video-rename/internal/domain"
)

type MarkFailure struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}
type MarkResult struct {
	Changed   int64         `json:"changed"`
	Unchanged int64         `json:"unchanged"`
	Failed    int           `json:"failed"`
	Failures  []MarkFailure `json:"failures"`
}

// SetProcessed walks current disk contents, not just previously indexed children.
// Each file is independently committed; bounded failure details expose partial success.
func (l *Library) SetProcessed(ctx context.Context, ids []int64, marked bool) MarkResult {
	result := MarkResult{Failures: []MarkFailure{}}
	seen := map[int64]bool{}
	fail := func(id int64, err error) {
		result.Failed++
		if len(result.Failures) < 100 {
			result.Failures = append(result.Failures, MarkFailure{id, l.DisplayPath(id), err.Error()})
		}
	}
	var walk func(int64)
	walk = func(id int64) {
		if seen[id] {
			return
		}
		seen[id] = true
		if err := ctx.Err(); err != nil {
			fail(id, err)
			return
		}
		e, err := l.Get(id)
		if err != nil {
			fail(id, err)
			return
		}
		if e.Kind == "file" && discovery.IsVideoExtension(e.Name) {
			n, err := l.setFileProcessed(ctx, id, marked)
			if err != nil {
				fail(id, err)
			} else if n > 0 {
				result.Changed += n
			} else {
				result.Unchanged++
			}
			return
		}
		if e.Kind != "directory" {
			return
		}
		if err = l.Refresh(ctx, id); err != nil {
			fail(id, err)
			return
		}
		var after int64
		for ctx.Err() == nil {
			rows, err := l.DB.QueryContext(ctx, "SELECT id FROM entries WHERE parent=? AND present=1 AND id>? ORDER BY id LIMIT 128", id, after)
			if err != nil {
				fail(id, err)
				return
			}
			children := []int64{}
			for rows.Next() {
				var child int64
				if err = rows.Scan(&child); err != nil {
					break
				}
				children = append(children, child)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				fail(id, err)
				return
			}
			if len(children) == 0 {
				return
			}
			for _, child := range children {
				walk(child)
				after = child
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
	for _, id := range ids {
		walk(id)
		if ctx.Err() != nil {
			break
		}
	}
	return result
}

func (l *Library) setFileProcessed(ctx context.Context, id int64, marked bool) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.CheckAvailable(id); err != nil {
		return 0, err
	}
	if l.Busy != nil && l.Busy(id) {
		return 0, fmt.Errorf("文件有活动任务，请等待任务结束后修改标记")
	}
	e, err := l.Get(id)
	if err != nil {
		return 0, err
	}
	p, err := l.Path(id)
	if err != nil {
		return 0, err
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		return 0, err
	}
	if Identity(snap) != e.Identity || snap.Size != e.Size || stamp(snap) != e.Mtime {
		return 0, fmt.Errorf("文件已变化，请刷新后重试")
	}
	query := "DELETE FROM marks WHERE identity=? AND size=? AND mtime=?"
	if marked {
		query = "INSERT INTO marks(identity,size,mtime,source,operation) VALUES(?,?,?,'marked',NULL) ON CONFLICT(identity,size,mtime) DO NOTHING"
	}
	res, err := l.DB.ExecContext(ctx, query, e.Identity, e.Size, e.Mtime)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
