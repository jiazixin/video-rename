// Package runstats aggregates batch performance for the 数据分析 tab: batch
// and item outcome rates, durations, throughput and failure groupings.
package runstats

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"video-rename/internal/storage"
	"video-rename/internal/timebucket"
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
	Task         string `json:"task"`
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
	Task         string `json:"task"`
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
	source                    string // manual or scheduled, see sourceKindSQL
	task                      string
	status, created           string
	started, finished         string
	startMs, finishedMs       int64
	durationMs                int64
	discovered                int64
	done, skipped, failed     int64
	errorText                 string
}

// itemRow is one item of a selected batch.
type itemRow struct {
	run                   int64
	status, message       string
	startedMs, finishedMs int64
}

// sourceKindSQL classifies a run as manual or scheduled. Filtering and the
// selected column share it so the two can never disagree.
const sourceKindSQL = `(CASE WHEN source='schedule' OR plan>0 THEN 'scheduled' ELSE 'manual' END)`

// itemQueryChunk bounds the IN list of one item query.
const itemQueryChunk = 500

// maxScatterPoints caps the size/duration view to the most recent batches.
const maxScatterPoints = 500

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

// parseConfigSummary reads the model identity a run recorded at creation.
// A malformed summary yields empty fields rather than failing the report.
func parseConfigSummary(summary string) (model, provider, protocol string) {
	var parsed struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Protocol string `json:"protocol"`
	}
	if summary == "" || json.Unmarshal([]byte(summary), &parsed) != nil {
		return "", "", ""
	}
	return parsed.Model, parsed.Provider, parsed.Protocol
}

// durationBands mirrors the fixed item-duration bands shown in the histogram.
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

// normalized fills defaults and clamps paging.
func (q AnalysisQuery) normalized() AnalysisQuery {
	if q.Location == nil {
		q.Location = time.UTC
	}
	if q.Sort == "" {
		q.Sort = "recent"
	}
	q.Page = max(q.Page, 1)
	if q.Size < 1 {
		q.Size = 20
	}
	q.Size = min(q.Size, 100)
	return q
}

// cacheKey identifies a full report independent of the requested page.
func (q AnalysisQuery) cacheKey() (string, error) {
	scope := q
	scope.Page, scope.Size, scope.Location = 0, 0, nil
	raw, err := json.Marshal(struct {
		Query    AnalysisQuery
		Timezone string
	}{scope, q.Location.String()})
	if err != nil {
		return "", err
	}
	return "runs:" + string(raw), nil
}

// Analytics aggregates batch performance inside one read transaction so every
// dimension of the report sees the same snapshot. Batches belong to the range
// by their actual start (created as fallback); items are attributed to their
// own recorded finish timestamp.
func Analytics(ctx context.Context, db *storage.Store, q AnalysisQuery) (*Report, error) {
	q = q.normalized()
	tx, err := db.Read().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	revision, err := storage.ReportRevision(ctx, tx, "runs")
	if err != nil {
		return nil, err
	}
	cacheKey, err := q.cacheKey()
	if err != nil {
		return nil, err
	}
	var cached Report
	if db.CachedReport(cacheKey, revision, &cached) {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return pageReport(ctx, &cached, q.Page, q.Size)
	}

	selected, err := selectRuns(ctx, tx, q)
	if err != nil {
		return nil, err
	}
	// "All history" starts at the first selected batch, mirroring the token
	// report, so bucket granularity follows the data's real extent.
	from := q.From
	if from == 0 {
		from = earliestStart(selected)
	}
	acc := newAccumulator(selected, timebucket.For(q.To-from, q.Location))
	for _, r := range selected {
		acc.addRun(r)
	}
	// Item details for the selected batches only; large ranges stay cheap.
	if err = scanItems(ctx, tx, selected, acc.addItem); err != nil {
		return nil, err
	}
	report := acc.report(q, from)
	if report.Options, err = reportOptions(ctx, db, tx); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	db.CacheReport(cacheKey, revision, report)
	return pageReport(ctx, report, q.Page, q.Size)
}

// selectRuns reads the batches in range that match the filters, in id order.
func selectRuns(ctx context.Context, tx *sql.Tx, q AnalysisQuery) ([]*runRow, error) {
	where := "analysis_start_ms>=? AND analysis_start_ms<?"
	args := []any{q.From, q.To}
	if q.ProfileSet || q.Profile > 0 {
		where += " AND model_config_id=?"
		args = append(args, q.Profile)
	}
	if q.Source != "" {
		where += " AND " + sourceKindSQL + "=?"
		args = append(args, q.Source)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,task,`+sourceKindSQL+`,config_summary,model_config_id,model_config_title,status,created,started,finished,discovered,done,skipped,failed,error FROM runs WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var selected []*runRow
	for rows.Next() {
		var r runRow
		var summary string
		if err = rows.Scan(&r.id, &r.task, &r.source, &summary, &r.profileID, &r.profileTitle, &r.status, &r.created, &r.started, &r.finished, &r.discovered, &r.done, &r.skipped, &r.failed, &r.errorText); err != nil {
			return nil, err
		}
		r.model, r.provider, r.protocol = parseConfigSummary(summary)
		startedMs := parseMs(r.started)
		r.startMs = startedMs
		if r.startMs == 0 {
			r.startMs = parseMs(r.created)
		}
		r.finishedMs = parseMs(r.finished)
		if startedMs > 0 && r.finishedMs > startedMs {
			r.durationMs = r.finishedMs - startedMs
		}
		selected = append(selected, &r)
	}
	return selected, rows.Err()
}

func earliestStart(runs []*runRow) int64 {
	var earliest int64
	for _, r := range runs {
		if earliest == 0 || r.startMs < earliest {
			earliest = r.startMs
		}
	}
	return earliest
}

// scanItems streams the items of the given batches to visit.
func scanItems(ctx context.Context, tx *sql.Tx, runs []*runRow, visit func(itemRow)) error {
	for chunk := range slices.Chunk(runs, itemQueryChunk) {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, r := range chunk {
			args[i] = r.id
		}
		if err := scanItemChunk(ctx, tx, `SELECT run,status,message,started_ms,finished_ms FROM run_items WHERE run IN (`+placeholders+`)`, args, visit); err != nil {
			return err
		}
	}
	return nil
}

func scanItemChunk(ctx context.Context, tx *sql.Tx, query string, args []any, visit func(itemRow)) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var it itemRow
		if err = rows.Scan(&it.run, &it.status, &it.message, &it.startedMs, &it.finishedMs); err != nil {
			return err
		}
		visit(it)
	}
	return rows.Err()
}

// trendAcc tallies one trend bucket.
type trendAcc struct {
	batches, batchSumMs   int64
	done, skipped, failed int64
}

// itemTiming tallies the timed items of one batch.
type itemTiming struct {
	count, sumMs int64
}

func (t itemTiming) averageMs() int64 {
	if t.count == 0 {
		return 0
	}
	return t.sumMs / t.count
}

// failureAcc tallies one normalized failure message while scanning.
type failureAcc struct {
	message string
	count   int64
	lastMs  int64
}

type failureGroups map[string]*failureAcc

func (groups failureGroups) add(message string, atMs int64) {
	key := normalizeMessage(message)
	group := groups[key]
	if group == nil {
		group = &failureAcc{message: key}
		groups[key] = group
	}
	group.count++
	group.lastMs = max(group.lastMs, atMs)
}

// accumulator folds selected batches and their items into report figures.
type accumulator struct {
	buckets          timebucket.Bucketing
	runs             []*runRow
	runByID          map[int64]*runRow
	totals           ReportTotals
	batchDurations   []int64
	itemDurations    []int64
	trend            map[int64]*trendAcc
	profiles         map[int64]*ProfilePerf
	profileDurations map[int64][]int64
	itemFailures     failureGroups
	batchFailures    failureGroups
	timingByRun      map[int64]itemTiming
}

func newAccumulator(runs []*runRow, buckets timebucket.Bucketing) *accumulator {
	acc := &accumulator{
		buckets:          buckets,
		runs:             runs,
		runByID:          make(map[int64]*runRow, len(runs)),
		trend:            map[int64]*trendAcc{},
		profiles:         map[int64]*ProfilePerf{},
		profileDurations: map[int64][]int64{},
		itemFailures:     failureGroups{},
		batchFailures:    failureGroups{},
		timingByRun:      map[int64]itemTiming{},
	}
	for _, r := range runs {
		acc.runByID[r.id] = r
	}
	return acc
}

// trendAt returns the trend bucket containing atMs.
func (acc *accumulator) trendAt(atMs int64) *trendAcc {
	key := acc.buckets.Start(atMs)
	bucket := acc.trend[key]
	if bucket == nil {
		bucket = &trendAcc{}
		acc.trend[key] = bucket
	}
	return bucket
}

// comparesModels reports whether a batch belongs in model comparisons.
// Extraction contributes to run totals, but never to model comparisons.
func comparesModels(r *runRow) bool {
	return r.task == "rename" && r.profileID > 0
}

func (acc *accumulator) addRun(r *runRow) {
	totals := &acc.totals
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
		acc.batchDurations = append(acc.batchDurations, r.durationMs)
		bucket := acc.trendAt(r.finishedMs)
		bucket.batches++
		bucket.batchSumMs += r.durationMs
	}
	if (r.status == "failed" || r.status == "interrupted") && r.errorText != "" {
		acc.batchFailures.add(r.errorText, r.finishedMs)
	}
	if !comparesModels(r) {
		return
	}
	profile := acc.profiles[r.profileID]
	if profile == nil {
		profile = &ProfilePerf{ID: r.profileID, Title: r.profileTitle, Model: r.model, Provider: r.provider, Protocol: r.protocol}
		acc.profiles[r.profileID] = profile
	}
	profile.Batches++
	profile.Done += r.done
	profile.Skipped += r.skipped
	profile.Failed += r.failed
}

func (acc *accumulator) addItem(it itemRow) {
	r := acc.runByID[it.run]
	if r == nil {
		return
	}
	if (it.status == "done" || it.status == "failed") && it.startedMs > 0 && it.finishedMs > it.startedMs {
		duration := it.finishedMs - it.startedMs
		acc.itemDurations = append(acc.itemDurations, duration)
		if comparesModels(r) {
			acc.profileDurations[r.profileID] = append(acc.profileDurations[r.profileID], duration)
		}
		timing := acc.timingByRun[it.run]
		acc.timingByRun[it.run] = itemTiming{count: timing.count + 1, sumMs: timing.sumMs + duration}
		acc.totals.TimedItems++
	}
	if it.finishedMs > 0 {
		switch it.status {
		case "done":
			acc.trendAt(it.finishedMs).done++
		case "skipped":
			acc.trendAt(it.finishedMs).skipped++
		case "failed":
			acc.trendAt(it.finishedMs).failed++
		}
	}
	if it.status == "failed" && it.message != "" {
		acc.itemFailures.add(it.message, it.finishedMs)
	}
}

func (acc *accumulator) report(q AnalysisQuery, from int64) *Report {
	totals := acc.totals
	totals.AvgBatchMs = average(acc.batchDurations)
	totals.AvgItemMs = average(acc.itemDurations)
	slices.Sort(acc.itemDurations)
	totals.P50ItemMs = percentile(acc.itemDurations, 0.50)
	totals.P95ItemMs = percentile(acc.itemDurations, 0.95)
	hours := float64(q.To-from) / 3_600_000
	if hours > 0 {
		totals.ThroughputPerHour = float64(totals.Done+totals.Skipped+totals.Failed) / hours
	}
	for id, profile := range acc.profiles {
		durations := acc.profileDurations[id]
		slices.Sort(durations)
		profile.AvgItemMs = average(durations)
		profile.P50ItemMs = percentile(durations, 0.50)
		profile.P95ItemMs = percentile(durations, 0.95)
		if hours > 0 {
			profile.ThroughputPerHour = float64(profile.Done+profile.Skipped+profile.Failed) / hours
		}
	}
	return &Report{
		Range: ReportRange{
			From:     time.UnixMilli(from).In(q.Location).Format(time.RFC3339),
			To:       time.UnixMilli(q.To).In(q.Location).Format(time.RFC3339),
			Timezone: q.Location.String(),
			Interval: acc.buckets.Name,
		},
		Totals:        totals,
		Trend:         acc.trendSeries(from, q.To),
		Distribution:  durationDistribution(acc.itemDurations),
		Profiles:      sortedProfiles(acc.profiles),
		Scatter:       scatterPoints(acc.runs),
		Failures:      sortedFailures(acc.itemFailures, 10),
		BatchFailures: sortedFailures(acc.batchFailures, 5),
		Batches:       acc.batchRows(q),
	}
}

func (acc *accumulator) trendSeries(from, to int64) []TrendBucket {
	series := make([]TrendBucket, 0, len(acc.trend))
	for start, end := range acc.buckets.Spans(from, to) {
		bucket := TrendBucket{From: acc.buckets.Format(start), To: acc.buckets.Format(end)}
		if tally := acc.trend[start]; tally != nil {
			bucket.FinishedBatches = tally.batches
			if tally.batches > 0 {
				bucket.AvgBatchMs = tally.batchSumMs / tally.batches
			}
			bucket.Done, bucket.Skipped, bucket.Failed = tally.done, tally.skipped, tally.failed
		}
		series = append(series, bucket)
	}
	return series
}

func durationDistribution(durations []int64) []DistBucket {
	distribution := make([]DistBucket, len(durationBands))
	for i, band := range durationBands {
		distribution[i] = DistBucket{Label: band.label}
	}
	for _, d := range durations {
		for i, band := range durationBands {
			if band.max == 0 || d < band.max {
				distribution[i].Count++
				break
			}
		}
	}
	return distribution
}

// scatterPoints keeps the most recent timed batches for the size/duration view.
func scatterPoints(runs []*runRow) []ScatterPoint {
	scatter := []ScatterPoint{}
	for _, r := range runs {
		if r.durationMs == 0 {
			continue
		}
		scatter = append(scatter, ScatterPoint{Run: r.id, Task: r.task, Items: r.discovered, Ms: r.durationMs, Status: r.status, Source: r.source, ProfileTitle: r.profileTitle})
	}
	slices.SortFunc(scatter, func(a, b ScatterPoint) int { return cmp.Compare(b.Run, a.Run) })
	if len(scatter) > maxScatterPoints {
		scatter = scatter[:maxScatterPoints]
	}
	return scatter
}

// batchRows lists every selected batch in the requested order; pageReport
// slices out the requested page.
func (acc *accumulator) batchRows(q AnalysisQuery) PagedBatches {
	ordered := sortedRuns(acc.runs, q.Sort)
	batches := PagedBatches{Items: make([]BatchRow, 0, len(ordered)), Total: len(ordered), Page: q.Page, Size: q.Size}
	for _, r := range ordered {
		batches.Items = append(batches.Items, BatchRow{ID: r.id, Task: r.task, Status: r.status, Source: r.source, ProfileID: r.profileID, ProfileTitle: r.profileTitle, Model: r.model, Provider: r.provider, Protocol: r.protocol, Created: r.created, Started: r.started, Finished: r.finished, Discovered: r.discovered, Done: r.done, Skipped: r.skipped, Failed: r.failed, DurationMs: r.durationMs, AvgItemMs: acc.timingByRun[r.id].averageMs()})
	}
	return batches
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

func sortedFailures(groups failureGroups, limit int) []FailureGroup {
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
func reportOptions(ctx context.Context, db *storage.Store, tx *sql.Tx) (ReportOptions, error) {
	revision, err := storage.ReportRevision(ctx, tx, "run_options")
	if err != nil {
		return ReportOptions{}, err
	}
	var cached ReportOptions
	if db.CachedReport("runs:options", revision, &cached) {
		return cached, nil
	}

	rows, err := tx.QueryContext(ctx, `SELECT model_config_id,model_config_title FROM runs WHERE id IN (SELECT MAX(id) FROM runs GROUP BY model_config_id) ORDER BY model_config_id`)
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
	if err := rows.Err(); err != nil {
		return ReportOptions{}, err
	}
	db.CacheReport("runs:options", revision, out)
	return out, nil
}

// The cached report includes the ordered batch rows; paging never re-reads items.
func pageReport(ctx context.Context, report *Report, page, size int) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := report.Batches.Items
	report.Batches.Page = min(page, max(1, (len(rows)+size-1)/size))
	report.Batches.Size = size
	start := (report.Batches.Page - 1) * size
	report.Batches.Items = rows[min(start, len(rows)):min(start+size, len(rows))]
	return report, nil
}
