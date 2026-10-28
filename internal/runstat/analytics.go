package runstat

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/pagination"
	"video-rename/internal/storage"
	"video-rename/internal/timebucket"
	"video-rename/internal/timefmt"
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
	Offset     int
	Limit      int
}

// Report is the batch performance report.
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

// ReportRange is the time range of a report.
type ReportRange struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
	Interval string `json:"interval"`
}

// ReportTotals summarizes the batches in a report.
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

// TrendBucket holds the batch outcomes of one time bucket.
type TrendBucket struct {
	From            string `json:"from"`
	To              string `json:"to"`
	FinishedBatches int64  `json:"finished_batches"`
	AvgBatchMs      int64  `json:"avg_batch_ms"`
	Done            int64  `json:"done"`
	Skipped         int64  `json:"skipped"`
	Failed          int64  `json:"failed"`
}

// DistBucket is one bar of a distribution.
type DistBucket struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// ProfilePerf is the performance of one model profile.
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

// ScatterPoint is one batch in the duration scatter chart.
type ScatterPoint struct {
	Run          int64  `json:"run"`
	Task         string `json:"task"`
	Items        int64  `json:"items"`
	Ms           int64  `json:"ms"`
	Status       string `json:"status"`
	Source       string `json:"source"`
	ProfileTitle string `json:"profile_title"`
}

// FailureGroup counts failures with one code and reason. Error is the first
// failure seen in the group.
type FailureGroup struct {
	Error  domain.Record `json:"error"`
	Count  int64         `json:"count"`
	LastAt string        `json:"last_at"`
}

// BatchRow is one batch in the report table.
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

// PagedBatches is a page of report batches.
type PagedBatches struct {
	Items  []BatchRow `json:"items"`
	Total  int        `json:"total"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
}

// ReportOptions lists the values available as report filters.
type ReportOptions struct {
	Profiles []OptionProfile `json:"profiles"`
}

// OptionProfile is a model profile available as a filter.
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
	parsed, err := time.Parse(timefmt.Layout, value)
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
// Labels are band codes the web client maps to text.
var durationBands = []struct {
	max   int64
	label string
}{
	{5_000, "under_5s"},
	{15_000, "5s_15s"},
	{30_000, "15s_30s"},
	{60_000, "30s_60s"},
	{120_000, "1m_2m"},
	{300_000, "2m_5m"},
	{600_000, "5m_10m"},
	{0, "over_10m"},
}

// failureKey groups a stored failure record by its code and reason.
func failureKey(record domain.Record) (string, domain.Record) {
	if record.Code == "" {
		record.Code = domain.OutcomeUnexpectedFailure
	}
	key := record.Code
	if reason, ok := record.Details["reason"].(string); ok {
		key += ":" + reason
	}
	return key, record
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
	q.Offset = max(q.Offset, 0)
	if q.Limit < 1 {
		q.Limit = 20
	}
	q.Limit = min(q.Limit, 100)
	return q
}

// cacheKey identifies a full report independent of the requested page.
func (q AnalysisQuery) cacheKey() (string, error) {
	scope := q
	scope.Offset, scope.Limit, scope.Location = 0, 0, nil
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
	return storage.ReadValue(ctx, db, func(tx *sql.Tx) (*Report, error) {
		q = q.normalized()
		var err error
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

			return pageReport(ctx, &cached, q.Offset, q.Limit)
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

		if err = ctx.Err(); err != nil {
			return nil, err
		}
		db.CacheReport(cacheKey, revision, report)
		return pageReport(ctx, report, q.Offset, q.Limit)
	})
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
	return storage.CollectRows(rows, func(rows *sql.Rows) (*runRow, error) {
		var r runRow
		var summary string
		if err := rows.Scan(&r.id, &r.task, &r.source, &summary, &r.profileID, &r.profileTitle, &r.status, &r.created, &r.started, &r.finished, &r.discovered, &r.done, &r.skipped, &r.failed, &r.errorText); err != nil {
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
		return &r, nil
	})
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
		if err := scanItemChunk(ctx, scanItemChunkOptions{Tx: tx, Query: `SELECT run,status,message,started_ms,finished_ms FROM run_items WHERE run IN (` + placeholders + `)`, Args: args, Visit: visit}); err != nil {
			return err
		}
	}
	return nil
}

// scanItemChunkOptions groups the inputs for scanItemChunk.
type scanItemChunkOptions struct {
	Tx    *sql.Tx
	Query string
	Args  []any
	Visit func(itemRow)
}

func scanItemChunk(ctx context.Context, params scanItemChunkOptions) (err error) {
	tx, query, args, visit := params.Tx, params.Query, params.Args, params.Visit

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
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
	record domain.Record
	count  int64
	lastMs int64
}

type failureGroups map[string]*failureAcc

func (g failureGroups) add(message string, atMs int64) {
	stored, err := domain.ParseRecord(message)
	if err != nil {
		// A record that cannot be read still counts as one internal failure.
		stored = domain.Record{Code: domain.OutcomeUnexpectedFailure}
	}
	key, record := failureKey(stored)
	group := g[key]
	if group == nil {
		group = &failureAcc{record: record}
		g[key] = group
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
func (a *accumulator) trendAt(atMs int64) *trendAcc {
	key := a.buckets.Start(atMs)
	bucket := a.trend[key]
	if bucket == nil {
		bucket = &trendAcc{}
		a.trend[key] = bucket
	}
	return bucket
}

// comparesModels reports whether a batch belongs in model comparisons.
// Extraction contributes to run totals, but never to model comparisons.
func comparesModels(r *runRow) bool {
	return r.task == "rename" && r.profileID > 0
}

func (a *accumulator) addRun(r *runRow) {
	totals := &a.totals
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
		a.batchDurations = append(a.batchDurations, r.durationMs)
		bucket := a.trendAt(r.finishedMs)
		bucket.batches++
		bucket.batchSumMs += r.durationMs
	}
	if (r.status == "failed" || r.status == "interrupted") && r.errorText != "" {
		a.batchFailures.add(r.errorText, r.finishedMs)
	}
	if !comparesModels(r) {
		return
	}
	profile := a.profiles[r.profileID]
	if profile == nil {
		profile = &ProfilePerf{ID: r.profileID, Title: r.profileTitle, Model: r.model, Provider: r.provider, Protocol: r.protocol}
		a.profiles[r.profileID] = profile
	}
	profile.Batches++
	profile.Done += r.done
	profile.Skipped += r.skipped
	profile.Failed += r.failed
}

func (a *accumulator) addItem(it itemRow) {
	r := a.runByID[it.run]
	if r == nil {
		return
	}
	if (it.status == "done" || it.status == "failed") && it.startedMs > 0 && it.finishedMs > it.startedMs {
		duration := it.finishedMs - it.startedMs
		a.itemDurations = append(a.itemDurations, duration)
		if comparesModels(r) {
			a.profileDurations[r.profileID] = append(a.profileDurations[r.profileID], duration)
		}
		timing := a.timingByRun[it.run]
		a.timingByRun[it.run] = itemTiming{count: timing.count + 1, sumMs: timing.sumMs + duration}
		a.totals.TimedItems++
	}
	if it.finishedMs > 0 {
		switch it.status {
		case "done":
			a.trendAt(it.finishedMs).done++
		case "skipped":
			a.trendAt(it.finishedMs).skipped++
		case "failed":
			a.trendAt(it.finishedMs).failed++
		}
	}
	if it.status == "failed" && it.message != "" {
		a.itemFailures.add(it.message, it.finishedMs)
	}
}

func (a *accumulator) report(q AnalysisQuery, from int64) *Report {
	totals := a.totals
	totals.AvgBatchMs = average(a.batchDurations)
	totals.AvgItemMs = average(a.itemDurations)
	slices.Sort(a.itemDurations)
	totals.P50ItemMs = percentile(a.itemDurations, 0.50)
	totals.P95ItemMs = percentile(a.itemDurations, 0.95)
	hours := float64(q.To-from) / 3_600_000
	if hours > 0 {
		totals.ThroughputPerHour = float64(totals.Done+totals.Skipped+totals.Failed) / hours
	}
	for id, profile := range a.profiles {
		durations := a.profileDurations[id]
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
			From:     timefmt.Format(time.UnixMilli(from)),
			To:       timefmt.Format(time.UnixMilli(q.To)),
			Timezone: q.Location.String(),
			Interval: a.buckets.Name,
		},
		Totals:        totals,
		Trend:         a.trendSeries(from, q.To),
		Distribution:  durationDistribution(a.itemDurations),
		Profiles:      sortedProfiles(a.profiles),
		Scatter:       scatterPoints(a.runs),
		Failures:      sortedFailures(a.itemFailures, 10),
		BatchFailures: sortedFailures(a.batchFailures, 5),
		Batches:       a.batchRows(q),
	}
}

func (a *accumulator) trendSeries(from, to int64) []TrendBucket {
	series := make([]TrendBucket, 0, len(a.trend))
	for start, end := range a.buckets.Spans(from, to) {
		bucket := TrendBucket{From: a.buckets.Format(start), To: a.buckets.Format(end)}
		if tally := a.trend[start]; tally != nil {
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
func (a *accumulator) batchRows(q AnalysisQuery) PagedBatches {
	ordered := sortedRuns(a.runs, q.Sort)
	batches := PagedBatches{Items: make([]BatchRow, 0, len(ordered)), Total: len(ordered), Offset: q.Offset, Limit: q.Limit}
	for _, r := range ordered {
		batches.Items = append(batches.Items, BatchRow{ID: r.id, Task: r.task, Status: r.status, Source: r.source, ProfileID: r.profileID, ProfileTitle: r.profileTitle, Model: r.model, Provider: r.provider, Protocol: r.protocol, Created: r.created, Started: r.started, Finished: r.finished, Discovered: r.discovered, Done: r.done, Skipped: r.skipped, Failed: r.failed, DurationMs: r.durationMs, AvgItemMs: a.timingByRun[r.id].averageMs()})
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
		out = append(out, FailureGroup{Error: group.record, Count: group.count, LastAt: timefmt.Format(time.UnixMilli(group.lastMs))})
	}
	slices.SortFunc(out, func(a, b FailureGroup) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Error.Code, b.Error.Code); c != 0 {
			return c
		}
		return cmp.Compare(a.Error.Message, b.Error.Message)
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
	out := ReportOptions{Profiles: []OptionProfile{}}
	out.Profiles, err = storage.CollectRows(rows, func(rows *sql.Rows) (OptionProfile, error) {
		var profile OptionProfile
		err := rows.Scan(&profile.ID, &profile.Title)
		return profile, err
	})
	if err != nil {
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
	report.Batches.Offset = pagination.Offset(page, len(rows), size)
	report.Batches.Limit = size
	start := report.Batches.Offset
	report.Batches.Items = rows[min(start, len(rows)):min(start+size, len(rows))]
	return report, nil
}
