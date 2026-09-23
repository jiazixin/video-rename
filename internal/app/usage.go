package app

import (
	"net/http"
	"strconv"
	"time"

	"video-rename/internal/tokenusage"
)

// usageAnalytics serves GET /api/usage/analytics. Times are RFC3339 in a
// half-open [from,to) range; from may be omitted to cover all recorded
// history and to defaults to now.
func (a *App) usageAnalytics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	now := time.Now().UTC()
	v := tokenusage.AnalyticsQuery{From: 0, To: now.UnixMilli(), Location: time.UTC, Page: 1, Size: 20}
	if raw := query.Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			send(w, 400, map[string]string{"message": "起始时间无效，请使用 RFC3339 格式"})
			return
		}
		v.From = parsed.UnixMilli()
	}
	if raw := query.Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			send(w, 400, map[string]string{"message": "结束时间无效，请使用 RFC3339 格式"})
			return
		}
		v.To = parsed.UnixMilli()
	}
	if v.From >= v.To {
		send(w, 400, map[string]string{"message": "结束时间必须晚于起始时间"})
		return
	}
	if name := query.Get("timezone"); name != "" {
		location, err := time.LoadLocation(name)
		if err != nil {
			send(w, 400, map[string]string{"message": "时区无效"})
			return
		}
		v.Location = location
	}
	v.Model = query.Get("model")
	var badRequest string
	for key, apply := range map[string]func(string){
		"source": func(value string) {
			if value != "" && value != "manual" && value != "scheduled" {
				badRequest = "运行来源无效"
				return
			}
			v.Source = value
		},
		"sort": func(value string) {
			if value != "" && value != "tokens" && value != "recent" {
				badRequest = "排序方式无效"
				return
			}
			v.Sort = value
		},
		"profile": func(value string) {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed < 0 {
				badRequest = "模型配置筛选无效"
				return
			}
			v.Profile = parsed
		},
		"run": func(value string) {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed < 0 {
				badRequest = "批次筛选无效"
				return
			}
			v.Run = parsed
		},
		"page": func(value string) {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 {
				badRequest = "页码无效"
				return
			}
			v.Page = parsed
		},
		"size": func(value string) {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 100 {
				badRequest = "每页数量无效"
				return
			}
			v.Size = parsed
		},
	} {
		if raw := query.Get(key); raw != "" {
			apply(raw)
			if badRequest != "" {
				send(w, 400, map[string]string{"message": badRequest})
				return
			}
		}
	}
	report, err := tokenusage.Analytics(a.DB, v)
	if err != nil {
		failure(w, err)
		return
	}
	send(w, 200, report)
}
