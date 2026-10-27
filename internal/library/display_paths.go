package library

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
)

// DisplayPaths loads the union of indexed ancestors in one read snapshot.
// These labels are not filesystem evidence; mutations must still use Path.
func (l *Library) DisplayPaths(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, ctx.Err()
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := l.DB.Read().QueryContext(ctx, `WITH RECURSIVE ancestors(id,parent,name) AS (
 SELECT e.id,e.parent,e.name FROM entries e JOIN json_each(?) selected ON e.id=selected.value
 UNION SELECT e.id,e.parent,e.name FROM entries e JOIN ancestors a ON e.id=a.parent
 ) SELECT id,parent,name FROM ancestors`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type node struct {
		parent int64
		name   string
	}
	nodes := map[int64]node{}
	for rows.Next() {
		var id int64
		var n node
		if err := rows.Scan(&id, &n.parent, &n.name); err != nil {
			return nil, err
		}
		nodes[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		parts := []string{}
		cur := id
		for cur > 1 && len(parts) < 1024 {
			n, ok := nodes[cur]
			if !ok {
				break
			}
			parts = append(parts, n.name)
			cur = n.parent
		}
		switch {
		case cur > 1:
			out[id] = MissingDisplayPath
		case len(parts) == 0:
			out[id] = RootDisplayPath
		default:
			slices.Reverse(parts)
			out[id] = strings.Join(parts, " / ")
		}
	}
	return out, ctx.Err()
}
