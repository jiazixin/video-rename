package app

import (
	"fmt"
	"net/http"
	"video-rename/internal/jobs"
)

// Retry submits the chosen model's current settings in one batch.
func (a *App) retryHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs           []int64 `json:"ids"`
		All           bool    `json:"all"`
		Key           string  `json:"key"`
		ModelConfigID int64   `json:"model_config_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	if (input.All && len(input.IDs) > 0) || (!input.All && len(input.IDs) == 0) || input.ModelConfigID < 0 || len(input.Key) < 8 || len(input.Key) > 120 {
		send(w, 400, map[string]string{"message": "请选择失败记录或重试全部失败记录，并提供有效模型配置和请求键"})
		return
	}
	accepted := []int64{}
	runs := []int64{}
	problems := []string{}
	skipped := 0
	type record struct{ id, entry int64 }
	records := []record{}
	if input.All {
		rows, err := a.DB.Read().QueryContext(r.Context(), "SELECT id,entry,action FROM events WHERE category='failure' ORDER BY id DESC")
		if err != nil {
			failure(w, err)
			return
		}
		for rows.Next() {
			var item record
			var action string
			if err = rows.Scan(&item.id, &item.entry, &action); err != nil {
				rows.Close()
				failure(w, err)
				return
			}
			if action != "automatic" || item.entry <= 0 {
				skipped++
				continue
			}
			records = append(records, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			failure(w, err)
			return
		}
	} else {
		for _, id := range input.IDs {
			item := record{id: id}
			err := a.DB.Read().QueryRowContext(r.Context(), "SELECT entry FROM events WHERE id=? AND category='failure' AND action='automatic' AND entry>0", id).Scan(&item.entry)
			if err != nil {
				problems = append(problems, fmt.Sprintf("记录 #%d 不存在或不支持自动重试", id))
				continue
			}
			records = append(records, item)
		}
	}
	type target struct {
		entry int64
		ids   []int64
	}
	items := []*target{}
	seen := map[int64]*target{}
	for _, record := range records {
		id, entry := record.id, record.entry
		if item, ok := seen[entry]; ok {
			item.ids = append(item.ids, id)
			continue
		}
		if _, err := a.Library.Path(entry); err != nil {
			problems = append(problems, fmt.Sprintf("记录 #%d：%s", id, err))
			continue
		}
		item := &target{entry: entry, ids: []int64{id}}
		seen[entry] = item
		items = append(items, item)
	}
	if len(items) > 0 {
		targets := make([]int64, 0, len(items))
		for _, item := range items {
			targets = append(targets, item.entry)
		}
		run, err := a.Jobs.Create(jobs.Request{Targets: targets, ModelConfigID: input.ModelConfigID, Key: input.Key}, "manual", 0)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%d 个文件未提交：%s", len(items), err))
		} else {
			runs = append(runs, run)
			for _, item := range items {
				accepted = append(accepted, item.ids...)
			}
		}
	}

	send(w, 200, map[string]any{"accepted": accepted, "runs": runs, "errors": problems, "skipped": skipped})
}
