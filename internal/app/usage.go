package app

import (
	"net/http"
	"strconv"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/tokenusage"
)

// usageAnalytics serves GET /api/usage/analytics. Times are RFC3339 in a
// half-open [from,to) range; from may be omitted to cover all recorded
// history and to defaults to now.
func (a *App) usageAnalytics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if err := queryFields(query, "from", "to", "timezone", "source", "sort", "profile", "run", "offset", "limit", "model"); err != nil {
		writeError(w, r, err)
		return
	}
	now := time.Now().UTC()
	v := tokenusage.AnalyticsQuery{From: 0, To: now.UnixMilli(), Location: time.UTC, Offset: 0, Limit: 20}
	if raw := query.Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, r, domain.InvalidField("from", "invalid_time"))
			return
		}
		v.From = parsed.UnixMilli()
	}
	if raw := query.Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, r, domain.InvalidField("to", "invalid_time"))
			return
		}
		v.To = parsed.UnixMilli()
	}
	if v.From >= v.To {
		writeError(w, r, domain.InvalidField("to", "not_after_from"))
		return
	}
	if name := query.Get("timezone"); name != "" {
		location, err := time.LoadLocation(name)
		if err != nil {
			writeError(w, r, domain.InvalidField("timezone", "invalid_timezone"))
			return
		}
		v.Location = location
	}
	v.Model = query.Get("model")
	var badRequest string
	for key, apply := range map[string]func(string){
		"source": func(value string) {
			if value != "" && value != "manual" && value != "scheduled" {
				badRequest = "invalid_choice"
				return
			}
			v.Source = value
		},
		"sort": func(value string) {
			if value != "" && value != "tokens" && value != "recent" {
				badRequest = "invalid_choice"
				return
			}
			v.Sort = value
		},
		"profile": func(value string) {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed < 0 {
				badRequest = "invalid_number"
				return
			}
			v.Profile = parsed
		},
		"run": func(value string) {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed < 0 {
				badRequest = "invalid_number"
				return
			}
			v.Run = parsed
		},
		"offset": func(value string) {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				badRequest = "invalid_number"
				return
			}
			v.Offset = parsed
		},
		"limit": func(value string) {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 100 {
				badRequest = "out_of_range"
				return
			}
			v.Limit = parsed
		},
	} {
		if raw := query.Get(key); raw != "" {
			apply(raw)
			if badRequest != "" {
				writeError(w, r, domain.InvalidField(key, badRequest))
				return
			}
		}
	}
	report, err := tokenusage.Analytics(r.Context(), a.db, v)
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, report)
}
