package app

import (
	"fmt"
	"net/http"
	"video-rename/internal/jobs"
)

// Retry uses the original model profile's current settings, allowing configuration fixes.
func (a *App) retryHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []int64 `json:"ids"`
		Key string  `json:"key"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.IDs) == 0 || len(input.IDs) > 200 || len(input.Key) < 8 || len(input.Key) > 120 {
		send(w, 400, map[string]string{"message": "请选择 1–200 条失败记录并提供请求键"})
		return
	}
	accepted := []int64{}
	runs := []int64{}
	problems := []string{}
	type target struct {
		entry int64
		ids   []int64
	}
	groups := map[int64][]*target{}
	models := []int64{}
	seen := map[int64]*target{}
	for _, id := range input.IDs {
		var entry, model int64
		err := a.DB.QueryRow("SELECT e.entry,COALESCE(r.model_config_id,0) FROM events e LEFT JOIN runs r ON r.id=e.run WHERE e.id=? AND e.category='failure' AND e.action='automatic' AND e.entry>0", id).Scan(&entry, &model)
		if err != nil {
			problems = append(problems, fmt.Sprintf("记录 #%d 不存在或不支持自动重试", id))
			continue
		}
		if item, ok := seen[entry]; ok {
			item.ids = append(item.ids, id)
			continue
		}
		if _, err = a.Library.Path(entry); err != nil {
			problems = append(problems, fmt.Sprintf("记录 #%d：%s", id, err))
			continue
		}
		item := &target{entry: entry, ids: []int64{id}}
		seen[entry] = item
		if _, ok := groups[model]; !ok {
			models = append(models, model)
		}
		groups[model] = append(groups[model], item)
	}
	for _, model := range models {
		items := groups[model]
		for offset := 0; offset < len(items); offset += 100 {
			batch := items[offset:min(offset+100, len(items))]
			targets := []int64{}
			for _, item := range batch {
				targets = append(targets, item.entry)
			}
			run, err := a.Jobs.Create(jobs.Request{Targets: targets, ModelConfigID: model, Key: fmt.Sprintf("%s-%d-%d", input.Key, model, offset)}, "manual", 0)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%d 个文件未提交：%s", len(batch), err))
				continue
			}
			runs = append(runs, run)
			for _, item := range batch {
				accepted = append(accepted, item.ids...)
			}
		}
	}
	send(w, 200, map[string]any{"accepted": accepted, "runs": runs, "errors": problems})
}
