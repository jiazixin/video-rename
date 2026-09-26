// Package runstats aggregates batch performance for the 数据分析 tab: batch
// and item outcome rates, durations, throughput and failure groupings.
package runstats

import (
	"cmp"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"video-rename/internal/storage"
)

// AnalysisQuery scopes a performance report. From and To are epoch
// milliseconds in a half-open range [From,To); a zero From means all recorded
// history. Filters apply to every dimension so tiles, trend and tables always
// describe the same slice.
type AnalysisQuery struct {
	From, To   int64
	Location   *time.Location
	Profile    int64
	ProfileSet bool   // distinguishes profile 0 (extraction) from no filter
	Source     string // "", manual or scheduled
	Sort       string // recent (default), duration or size
	Page       int
	Size       int
}

type Report struct {
	Range         ReportRange    `json:"range"`
	Totals        ReportTotals   `json:"totals"`
	Trend         []TrendBucket  `json:"trend"`
	Distribution  []DistBucket   `json:"distribution"`
	Profiles      []ProfilePerf  `json:"profiles"`
	Scatter       []ScatterPoint `json:"scatter"`
	Failures      []FailureGroup `json:"failures"`
	BatchFailures []FailureGroup `json:"batch_failures"`
	Batches       PagedBatches   `json:"batches"`
	Options       ReportOptions  `json:"options"`
}
type ReportRange struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
	Interval string `json:"interval"`
}
type ReportTotals struct {
	Batches           int64   `json:"batches"`
	Completed         int64   `json:"completed"`
	PartialFailure    int64   `json:"partial_failure"`
	FailedBatches     int64   `json:"failed_batches"`
	Cancelled         int64   `json:"cancelled"`
	Interrupted       int64   `json:"interrupted"`
	Active            int64   `json:"active"`
	Done              int64   `json:"done"`
	Skipped           int64   `json:"skipped"`
	Failed            int64   `json:"failed"`
	TimedItems        int64   `json:"timed_items"`
	AvgBatchMs        int64   `json:"avg_batch_ms"`
	AvgItemMs         int64   `json:"avg_item_ms"`
	P50ItemMs         int64   `json:"p50_item_ms"`
	P95ItemMs         int64   `json:"p95_item_ms"`
	ThroughputPerHour float64 `json:"throughput_per_hour"`
}
type TrendBucket struct {
	From            string `json:"from"`
	To              string `json:"to"`
	FinishedBatches int64  `json:"finished_batches"`
	AvgBatchMs      int64  `json:"avg_batch_ms"`
	Done            int64  `json:"done"`
	Skipped         int64  `json:"skipped"`
	Failed          int64  `json:"failed"`
}
type DistBucket struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}
type ProfilePerf struct {
	ID                int64   `json:"id"`
	Title             string  `json:"title"`
	Model             string  `json:"model"`
	Provider          string  `json:"provider"`
	Protocol          string  `json:"protocol"`
	Batches           int64   `json:"batches"`
	Done              int64   `json:"done"`
	Skipped           int64   `json:"skipped"`
	Failed            int64   `json:"failed"`
	AvgItemMs         int64   `json:"avg_item_ms"`
	P50ItemMs         int64   `json:"p50_item_ms"`
	P95ItemMs         int64   `json:"p95_item_ms"`
	ThroughputPerHour float64 `json:"throughput_per_hour"`
}
type ScatterPoint struct {
	Run          int64  `json:"run"`
	Items        int64  `json:"items"`
	Ms           int64  `json:"ms"`
	Status       string `json:"status"`
	Source       string `json:"source"`
	ProfileTitle string `json:"profile_title"`
}
type FailureGroup struct {
	Message string `json:"message"`
	Count   int64  `json:"count"`
	LastAt  string `json:"last_at"`
}
type BatchRow struct {
	ID           int64  `json:"id"`
	Status       string `json:"status"`
	Source       string `json:"source"`
	ProfileID    int64  `json:"profile_id"`
	ProfileTitle string `json:"profile_title"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	Protocol     string `json:"protocol"`
	Created      string `json:"created"`
	Started      string `json:"started"`
	Finished     string `json:"finished"`
	Discovered   int64  `json:"discovered"`
	Done         int64  `json:"done"`
	Skipped      int64  `json:"skipped"`
	Failed       int64  `json:"failed"`
	DurationMs   int64  `json:"duration_ms"`
	AvgItemMs    int64  `json:"avg_item_ms"`
}
type PagedBatches struct {
	Items []BatchRow `json:"items"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}
type ReportOptions struct {
	Profiles []OptionProfile `json:"profiles"`
}
type OptionProfile struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// runRow is one selected batch with parsed timing. startMs falls back to
// created for batches that never actually started; duration stays zero when
// either endpoint is missing.
type runRow struct {
	id                        int64
	profileID                 int64
	profileTitle              string
	model, provider, protocol string
	source                    string
	status, created           string
	started, finished         string
	startMs, finishedMs       int64
	durationMs                int64
	discovered                int64
	done, skipped, failed     int64
	errorText                 string
}

func terminal(status string) bool {
	switch status {
	case "completed", "partial_failure", "failed", "cancelled", "interrupted":
		return true
	}
	return false
}

func parseMs(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

func parseMeta(source string, plan int64, summary string, profileID int64, profileTitle string) (string, string, string, string) {
	var parsed struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Protocol string `json:"protocol"`
	}
	if summary != "" && json.Unmarshal([]byte(summary), &parsed) != nil {
		parsed.Model, parsed.Provider, parsed.Protocol = "", "", ""
	}
	kind := "manual"
	if source == "schedule" || plan > 0 {
		kind = "scheduled"
	}
	return parsed.Model, parsed.Provider, parsed.Protocol, kind
}

// durationLabels mirrors the fixed item-duration bands shown in the histogram.
var durationBands = []struct {
	max   int64
	label string
}{
	{5_000, "5 秒内"},
	{15_000, "5–15 秒"},
	{30_000, "15–30 秒"},
	{60_000, "30–60 秒"},
	{120_000, "1–2 分钟"},
	{300_000, "2–5 分钟"},
	{600_000, "5–10 分钟"},
	{0, "10 分钟以上"},
}

// normalizeMessage groups raw error text: keep the first line, trim and cap
// the length so near-identical failures collapse into one bucket.
func normalizeMessage(message string) string {
	message = strings.SplitN(message, "\n", 2)[0]
	message = strings.TrimSpace(message)
	runes := []rune(message)
	if len(runes) > 100 {
		runes = append(runes[:100], '…')
	}
	if len(runes) == 0 {
		return "未提供失败原因"
	}
	return string(runes)
}

func percentile(sorted []int64, fraction float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := min(max(int(math.Ceil(fraction*float64(len(sorted))))-1, 0), len(sorted)-1)
	return sorted[index]
}

func average(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	var sum int64
	for _, v := range values {
		sum += v
	}
	return sum / int64(len(values))
}

// Analytics aggregates batch performance inside one read transaction so every
// dimension of the report sees the same snapshot. Batches belong to the range
// by their actual start (created as fallback); items are attributed to their
// own recorded finish timestamp.
func Analytics(db *storage.Store, q AnalysisQuery) (*Report, error) {
	if q.Location == nil {
		q.Location = time.UTC
	}
	if q.Sort == "" {
		q.Sort = "recent"
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		if q.Size < 1 {
			q.Size = 20
		} else {
			q.Size = 100
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,source,plan,config_summary,model_config_id,model_config_title,status,created,started,finished,discovered,done,skipped,failed,error FROM runs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var selected []*runRow
	for rows.Next() {
		var r runRow
		var plan, profileID int64
		var source, summary, title string
		if err = rows.Scan(&r.id, &source, &plan, &summary, &profileID, &title, &r.status, &r.created, &r.started, &r.finished, &r.discovered, &r.done, &r.skipped, &r.failed, &r.errorText); err != nil {
			rows.Close()
			return nil, err
		}
		r.model, r.provider, r.protocol, r.source = parseMeta(source, plan, summary, profileID, title)
		r.profileID, r.profileTitle = profileID, title
		r.startMs = parseMs(r.started)
		if r.startMs == 0 {
			r.startMs = parseMs(r.created)
		}
		r.finishedMs = parseMs(r.finished)
		if started := parseMs(r.started); started > 0 && r.finishedMs > started {
			r.durationMs = r.finishedMs - started
		}
		if ((q.ProfileSet || q.Profile > 0) && r.profileID != q.Profile) || (q.Source != "" && r.source != q.Source) {
			continue
		}
		if r.startMs < q.From || r.startMs >= q.To {
			continue
		}
		selected = append(selected, &r)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// "All history" starts at the first selected batch, mirroring the token
	// report, so bucket granularity follows the data's real extent.
	from := q.From
	if from == 0 {
		for _, r := range selected {
			if from == 0 || r.startMs < from {
				from = r.startMs
			}
		}
	}
	buckets := bucketingFor(q.To-from, q.Location)

	// Item details for the selected batches only; large ranges stay cheap.
	type itemRow struct {
		run                       int64
		status, message           string
		startedMs, finishedMsItem int64
	}
	var items []itemRow
	ids := make([]int64, 0, len(selected))
	for _, r := range selected {
		ids = append(ids, r.id)
	}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		chunk := ids[start:end]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		itemRows, err := tx.Query(`SELECT run,status,message,started_ms,finished_ms FROM run_items WHERE run IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for itemRows.Next() {
			var it itemRow
			if err = itemRows.Scan(&it.run, &it.status, &it.message, &it.startedMs, &it.finishedMsItem); err != nil {
				itemRows.Close()
				return nil, err
			}
			items = append(items, it)
		}
		if err = itemRows.Err(); err != nil {
			itemRows.Close()
			return nil, err
		}
		itemRows.Close()
	}
	byRun := map[int64]*runRow{}
	for _, r := range selected {
		byRun[r.id] = r
	}

	var totals ReportTotals
	var batchDurations []int64
	var itemDurations []int64
	type trendAcc struct {
		batches, batchSum     int64
		done, skipped, failed int64
	}
	trend := map[int64]*trendAcc{}
	profileStats := map[int64]*ProfilePerf{}
	profileDurations := map[int64][]int64{}
	failures := map[string]*failureAcc{}
	batchFailures := map[string]*failureAcc{}
	avgPerRun := map[int64][2]int64{} // run id → [timed count, duration sum]

	for _, r := range selected {
		totals.Batches++
		switch {
		case r.status == "completed":
			totals.Completed++
		case r.status == "partial_failure":
			totals.PartialFailure++
		case r.status == "failed":
			totals.FailedBatches++
		case r.status == "cancelled":
			totals.Cancelled++
		case r.status == "interrupted":
			totals.Interrupted++
		case !terminal(r.status):
			totals.Active++
		}
		totals.Done += r.done
		totals.Skipped += r.skipped
		totals.Failed += r.failed
		if r.durationMs > 0 {
			batchDurations = append(batchDurations, r.durationMs)
			key := buckets.start(r.finishedMs)
			acc := trend[key]
			if acc == nil {
				acc = &trendAcc{}
				trend[key] = acc
			}
			acc.batches++
			acc.batchSum += r.durationMs
		}
		if (r.status == "failed" || r.status == "interrupted") && r.errorText != "" {
			key := normalizeMessage(r.errorText)
			group := batchFailures[key]
			if group == nil {
				group = &failureAcc{message: key}
				batchFailures[key] = group
			}
			group.count++
			if r.finishedMs > group.lastMs {
				group.lastMs = r.finishedMs
			}
		}
		profile := profileStats[r.profileID]
		if profile == nil {
			profile = &ProfilePerf{ID: r.profileID, Title: r.profileTitle, Model: r.model, Provider: r.provider, Protocol: r.protocol}
			profileStats[r.profileID] = profile
		}
		profile.Batches++
		profile.Done += r.done
		profile.Skipped += r.skipped
		profile.Failed += r.failed
	}

	for _, it := range items {
		r := byRun[it.run]
		if r == nil {
			continue
		}
		if it.status == "done" || it.status == "failed" {
			if it.startedMs > 0 && it.finishedMsItem > it.startedMs {
				itemDurations = append(itemDurations, it.finishedMsItem-it.startedMs)
				profileDurations[r.profileID] = append(profileDurations[r.profileID], it.finishedMsItem-it.startedMs)
				count, sum := avgPerRun[it.run][0], avgPerRun[it.run][1]
				avgPerRun[it.run] = [2]int64{count + 1, sum + it.finishedMsItem - it.startedMs}
				totals.TimedItems++
			}
		}
		if it.status == "done" || it.status == "skipped" || it.status == "failed" {
			at := it.finishedMsItem
			if at > 0 {
				key := buckets.start(at)
				acc := trend[key]
				if acc == nil {
					acc = &trendAcc{}
					trend[key] = acc
				}
				switch it.status {
				case "done":
					acc.done++
				case "skipped":
					acc.skipped++
				case "failed":
					acc.failed++
				}
			}
		}
		if it.status == "failed" && it.message != "" {
			key := normalizeMessage(it.message)
			group := failures[key]
			if group == nil {
				group = &failureAcc{message: key}
				failures[key] = group
			}
			group.count++
			if it.finishedMsItem > group.lastMs {
				group.lastMs = it.finishedMsItem
			}
		}
	}

	totals.AvgBatchMs = average(batchDurations)
	totals.AvgItemMs = average(itemDurations)
	slices.Sort(itemDurations)
	totals.P50ItemMs = percentile(itemDurations, 0.50)
	totals.P95ItemMs = percentile(itemDurations, 0.95)
	hours := float64(q.To-from) / 3_600_000
	if hours > 0 {
		totals.ThroughputPerHour = float64(totals.Done+totals.Skipped+totals.Failed) / hours
	}
	for id, profile := range profileStats {
		durations := profileDurations[id]
		slices.Sort(durations)
		profile.AvgItemMs = average(durations)
		profile.P50ItemMs = percentile(durations, 0.50)
		profile.P95ItemMs = percentile(durations, 0.95)
		if hours > 0 {
			profile.ThroughputPerHour = float64(profile.Done+profile.Skipped+profile.Failed) / hours
		}
	}

	series := make([]TrendBucket, 0, len(trend))
	end := buckets.start(q.To - 1)
	for cursor, guard := buckets.start(from), 0; cursor <= end && guard < 2000; cursor, guard = buckets.next(cursor), guard+1 {
		bucket := TrendBucket{From: time.UnixMilli(cursor).In(q.Location).Format(time.RFC3339), To: time.UnixMilli(buckets.next(cursor)).In(q.Location).Format(time.RFC3339)}
		if acc := trend[cursor]; acc != nil {
			bucket.FinishedBatches = acc.batches
			if acc.batches > 0 {
				bucket.AvgBatchMs = acc.batchSum / acc.batches
			}
			bucket.Done, bucket.Skipped, bucket.Failed = acc.done, acc.skipped, acc.failed
		}
		series = append(series, bucket)
	}

	distribution := make([]DistBucket, len(durationBands))
	for i, band := range durationBands {
		distribution[i] = DistBucket{Label: band.label}
	}
	for _, d := range itemDurations {
		for i, band := range durationBands {
			if band.max == 0 || d < band.max {
				distribution[i].Count++
				break
			}
		}
	}

	scatter := []ScatterPoint{}
	for _, r := range selected {
		if r.durationMs == 0 {
			continue
		}
		scatter = append(scatter, ScatterPoint{Run: r.id, Items: r.discovered, Ms: r.durationMs, Status: r.status, Source: r.source, ProfileTitle: r.profileTitle})
	}
	// Most recent batches matter most for the size/duration view; cap the payload.
	slices.SortFunc(scatter, func(a, b ScatterPoint) int { return cmp.Compare(b.Run, a.Run) })
	if len(scatter) > 500 {
		scatter = scatter[:500]
	}

	report := &Report{
		Range: ReportRange{
			From:     time.UnixMilli(from).In(q.Location).Format(time.RFC3339),
			To:       time.UnixMilli(q.To).In(q.Location).Format(time.RFC3339),
			Timezone: q.Location.String(),
			Interval: buckets.name,
		},
		Totals:        totals,
		Trend:         series,
		Distribution:  distribution,
		Profiles:      sortedProfiles(profileStats),
		Scatter:       scatter,
		Failures:      sortedFailures(failures, 10),
		BatchFailures: sortedFailures(batchFailures, 5),
		Batches:       PagedBatches{Items: []BatchRow{}, Page: q.Page, Size: q.Size},
	}
	ordered := sortedRuns(selected, q.Sort)
	report.Batches.Total = len(ordered)
	report.Batches.Page = min(q.Page, max(1, (len(ordered)+q.Size-1)/q.Size))
	startIndex := (report.Batches.Page - 1) * q.Size
	for i := startIndex; i < len(ordered) && i < startIndex+q.Size; i++ {
		r := ordered[i]
		var avg int64
		if count, sum := avgPerRun[r.id][0], avgPerRun[r.id][1]; count > 0 {
			avg = sum / count
		}
		report.Batches.Items = append(report.Batches.Items, BatchRow{ID: r.id, Status: r.status, Source: r.source, ProfileID: r.profileID, ProfileTitle: r.profileTitle, Model: r.model, Provider: r.provider, Protocol: r.protocol, Created: r.created, Started: r.started, Finished: r.finished, Discovered: r.discovered, Done: r.done, Skipped: r.skipped, Failed: r.failed, DurationMs: r.durationMs, AvgItemMs: avg})
	}
	options, err := reportOptions(tx)
	if err != nil {
		return nil, err
	}
	report.Options = options
	return report, tx.Commit()
}

// failureAcc tallies one normalized failure message while scanning.
type failureAcc struct {
	message string
	count   int64
	lastMs  int64
}

func sortedProfiles(profiles map[int64]*ProfilePerf) []ProfilePerf {
	out := make([]ProfilePerf, 0, len(profiles))
	for profile := range maps.Values(profiles) {
		out = append(out, *profile)
	}
	slices.SortFunc(out, func(a, b ProfilePerf) int {
		if c := cmp.Compare(b.Batches, a.Batches); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

func sortedFailures(groups map[string]*failureAcc, limit int) []FailureGroup {
	out := make([]FailureGroup, 0, len(groups))
	for group := range maps.Values(groups) {
		out = append(out, FailureGroup{Message: group.message, Count: group.count, LastAt: time.UnixMilli(group.lastMs).UTC().Format(time.RFC3339)})
	}
	slices.SortFunc(out, func(a, b FailureGroup) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.Message, b.Message)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sortedRuns(runs []*runRow, sortKey string) []*runRow {
	out := slices.Clone(runs)
	slices.SortFunc(out, func(a, b *runRow) int {
		switch sortKey {
		case "duration":
			if c := cmp.Compare(b.durationMs, a.durationMs); c != 0 {
				return c
			}
		case "size":
			if c := cmp.Compare(b.discovered, a.discovered); c != 0 {
				return c
			}
		default: // recent
			if c := cmp.Compare(b.startMs, a.startMs); c != 0 {
				return c
			}
		}
		return cmp.Compare(b.id, a.id)
	})
	return out
}

// reportOptions lists model profiles that ever created a run so the page can
// offer filters even when the current range shows no batches for them.
func reportOptions(tx *sql.Tx) (ReportOptions, error) {
	rows, err := tx.Query(`SELECT model_config_id,model_config_title FROM runs WHERE id IN (SELECT MAX(id) FROM runs GROUP BY model_config_id) ORDER BY model_config_id`)
	if err != nil {
		return ReportOptions{}, err
	}
	defer rows.Close()
	out := ReportOptions{Profiles: []OptionProfile{}}
	for rows.Next() {
		var id int64
		var title string
		if err = rows.Scan(&id, &title); err != nil {
			return ReportOptions{}, err
		}
		out.Profiles = append(out.Profiles, OptionProfile{ID: id, Title: title})
	}
	return out, rows.Err()
}

// bucketing picks a trend granularity for the requested span. Calendar
// buckets align to local midnights so day and month boundaries follow the
// reader's timezone, including DST transitions; the mechanics mirror
// internal/tokenusage.
type bucketing struct {
	name string
	step int64 // fixed-step milliseconds; zero for calendar buckets
	loc  *time.Location
}

func bucketingFor(span int64, loc *time.Location) bucketing {
	const hour, day = int64(time.Hour / time.Millisecond), int64(24 * time.Hour / time.Millisecond)
	switch {
	case span <= 6*hour:
		return bucketing{name: "15m", step: 15 * 60 * 1000, loc: loc}
	case span <= 72*hour:
		return bucketing{name: "hour", step: hour, loc: loc}
	case span <= 120*day:
		return bucketing{name: "day", loc: loc}
	case span <= 10*365*day:
		return bucketing{name: "month", loc: loc}
	default:
		return bucketing{name: "year", loc: loc}
	}
}

func align(ms, size int64) int64 {
	return ms - ms%size
}

func (b bucketing) start(ms int64) int64 {
	t := time.UnixMilli(ms).In(b.loc)
	switch b.name {
	case "15m", "hour":
		return align(ms, b.step)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, b.loc).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, b.loc).UnixMilli()
	default:
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, b.loc).UnixMilli()
	}
}

func (b bucketing) next(ms int64) int64 {
	t := time.UnixMilli(ms).In(b.loc)
	switch b.name {
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, b.loc).AddDate(0, 0, 1).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, b.loc).AddDate(0, 1, 0).UnixMilli()
	case "year":
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, b.loc).AddDate(1, 0, 0).UnixMilli()
	default:
		return ms + b.step
	}
}
