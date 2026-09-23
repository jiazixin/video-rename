package jobs

import (
	"encoding/json/v2"
	"fmt"
	"video-rename/internal/library"
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
	covers := func(a, b library.Entry, recursive bool) bool {
		if a.ID == b.ID || a.Kind == "file" && b.Kind == "file" && a.Identity == b.Identity {
			return true
		}
		return a.Kind == "directory" && (recursive && m.Library.IsWithin(b.ID, a.ID) || b.Kind == "file" && b.Parent == a.ID)
	}
	for _, id := range request.Targets {
		a, err := m.Library.Get(id)
		if err != nil {
			return err
		}
		for _, s := range scopes {
			for _, other := range s.targets {
				b, err := m.Library.Get(other)
				if err != nil {
					return fmt.Errorf("批次 #%d 的目标状态不可用，请等待结束或取消后重试", s.id)
				}
				if covers(a, b, request.Recursive) || covers(b, a, s.recursive) {
					return fmt.Errorf("所选范围与批次 #%d 重叠，该批次尚未结束，请勿重复提交", s.id)
				}
			}
		}
	}
	return nil
}
