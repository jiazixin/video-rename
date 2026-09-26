package jobs

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
)

// All callers create batches through Create's mutex, including scheduled runs.
func (m *Manager) checkOverlap(request Request) error {
	rows, err := m.DB.Query("SELECT id,targets,recursive FROM runs WHERE status IN ('queued','scanning','running','cancelling') ORDER BY id")
	if err != nil {
		return err
	}
	type scope struct {
		id        int64
		targets   []int64
		recursive bool
	}
	var scopes []scope
	for rows.Next() {
		var s scope
		var raw string
		if err = rows.Scan(&s.id, &raw, &s.recursive); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &s.targets); err != nil {
			break
		}
		scopes = append(scopes, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// 每个条目只读取一次轻量字段,再在内存中做两两比较;
	// 之前的写法对每个目标对都执行完整 Get(含运行状态与操作历史子查询),
	// 目标数增长时呈平方级放大,拖慢批量提交。
	type target struct {
		id, parent int64
		kind       string
		identity   string
	}
	unique := map[int64]struct{}{}
	for _, id := range request.Targets {
		unique[id] = struct{}{}
	}
	for _, s := range scopes {
		for _, id := range s.targets {
			unique[id] = struct{}{}
		}
	}
	entries := make(map[int64]target, len(unique))
	for id := range maps.Keys(unique) {
		var t target
		if err := m.DB.QueryRow("SELECT id,parent,kind,identity FROM entries WHERE id=? AND present=1", id).Scan(&t.id, &t.parent, &t.kind, &t.identity); err != nil {
			for _, s := range scopes {
				if slices.Contains(s.targets, id) {
					return fmt.Errorf("批次 #%d 的目标状态不可用，请等待结束或取消后重试", s.id)
				}
			}
			return err
		}
		entries[id] = t
	}
	covers := func(a, b target, recursive bool) bool {
		if a.id == b.id || a.kind == "file" && b.kind == "file" && a.identity == b.identity {
			return true
		}
		return a.kind == "directory" && (recursive && m.Library.IsWithin(b.id, a.id) || b.kind == "file" && b.parent == a.id)
	}
	for _, id := range request.Targets {
		a := entries[id]
		for _, s := range scopes {
			for _, other := range s.targets {
				if covers(a, entries[other], request.Recursive) || covers(entries[other], a, s.recursive) {
					return fmt.Errorf("所选范围与批次 #%d 重叠，该批次尚未结束，请勿重复提交", s.id)
				}
			}
		}
	}
	return nil
}
