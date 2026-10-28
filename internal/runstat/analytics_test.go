package runstat

import (
	"cmp"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

func analyticsDB(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "runstats.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

type seedItem struct {
	status   string
	message  string
	started  int64 // epoch ms; zero = never recorded
	finished int64
}

type seedBatch struct {
	id                                 int64
	source                             string
	task                               string
	plan                               int64
	profile                            int64
	title                              string
	status, created, started, finished string
	discovered, done, skipped, failed  int64
	err                                string
	items                              []seedItem
}

func at(t time.Time) string { return timefmt.Format(t) }

func seedBatch2(t *testing.T, db *storage.Store, b seedBatch) {
	t.Helper()
	summary := `{"model":"vision","provider":"custom","protocol":"openai"}`
	if b.task == "extract" {
		summary = `{}`
	}
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(id,task,source,targets,recursive,status,created,started,finished,discovered,done,skipped,failed,error,request_key,plan,config_summary,model_config_id,model_config_title) VALUES(?,?,?,'[]',1,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			b.id, cmp.Or(b.task, "rename"), b.source, b.status, b.created, b.started, b.finished, b.discovered, b.done, b.skipped, b.failed, b.err, fmt.Sprint("key-", b.id), b.plan, summary, b.profile, b.title)
	}); err != nil {
		t.Fatal(err)
	}
	for i, item := range b.items {
		entry := b.id*1000 + int64(i) + 1
		if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), `INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(?,1,?,'file',?,1,0,'o')`, entry, fmt.Sprint("name-", entry), fmt.Sprint("identity-", entry))
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), `INSERT INTO run_items(id,run,entry,identity,size,mtime_ns,status,message,started_ms,finished_ms) VALUES(?,?,?,?,1,0,?,?,?,?)`,
				b.id*100+int64(i)+1, b.id, entry, fmt.Sprint("identity-", entry), item.status, item.message, item.started, item.finished)
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// seededDashboard covers every metric: timed and untimed items, all batch
// statuses, a batch outside the range and a profile only visible in options.
func seededDashboard(t *testing.T, db *storage.Store, base time.Time) {
	t.Helper()
	seedBatch2(t, db, seedBatch{id: 1, source: "manual", profile: 1, title: "配置一", status: "completed",
		created: at(base), started: at(base.Add(2 * time.Hour)), finished: at(base.Add(3 * time.Hour)),
		discovered: 3, done: 2, skipped: 1,
		items: []seedItem{
			{status: "done", started: base.Add(2*time.Hour + 5*time.Minute).UnixMilli(), finished: base.Add(2*time.Hour + 5*time.Minute + 10*time.Second).UnixMilli()},
			{status: "done", started: base.Add(2*time.Hour + 20*time.Minute).UnixMilli(), finished: base.Add(2*time.Hour + 20*time.Minute + 25*time.Second).UnixMilli()},
			{status: "skipped", finished: base.Add(2 * time.Minute).UnixMilli()},
		}})
	seedBatch2(t, db, seedBatch{id: 2, source: "schedule", plan: 3, profile: 2, title: "配置二", status: "partial_failure",
		created: at(base.Add(time.Hour)), started: at(base.Add(90 * time.Minute)), finished: at(base.Add(2 * time.Hour)),
		discovered: 2, done: 1, failed: 1,
		items: []seedItem{
			{status: "done", started: base.Add(91 * time.Minute).UnixMilli(), finished: base.Add(91*time.Minute + 40*time.Second).UnixMilli()},
			{status: "failed", message: `{"code":"naming_failed","message":"model output named a failure\ndetail"}`, started: base.Add(105 * time.Minute).UnixMilli(), finished: base.Add(105*time.Minute + 5*time.Second).UnixMilli()},
		}})
	seedBatch2(t, db, seedBatch{id: 3, source: "manual", profile: 1, title: "配置一", status: "cancelled",
		created: at(base.Add(time.Hour)), started: at(base.Add(130 * time.Minute)), finished: at(base.Add(140 * time.Minute)),
		discovered: 1, failed: 1,
		items: []seedItem{{status: "failed", message: `{"code":"extract_failed","message":"failed before cancel"}`, finished: base.Add(140 * time.Minute).UnixMilli()}}})
	seedBatch2(t, db, seedBatch{id: 4, source: "manual", profile: 1, title: "配置一", status: "failed", err: `{"code":"dependency_failed","message":"global dependency failure"}`,
		created: at(base.Add(time.Hour)), started: at(base.Add(150 * time.Minute)), finished: at(base.Add(151 * time.Minute)),
		discovered: 1, failed: 1,
		items: []seedItem{{status: "failed", message: `{"code":"naming_failed","message":"model output named a failure"}`, finished: base.Add(151 * time.Minute).UnixMilli()}}})
	// Outside the requested range: fully excluded.
	seedBatch2(t, db, seedBatch{id: 5, source: "manual", profile: 3, title: "配置三", status: "completed",
		created: at(base.Add(-time.Hour)), started: at(base.Add(-time.Hour)), finished: at(base.Add(-30 * time.Minute)),
		discovered: 1, done: 1, items: []seedItem{{status: "done"}}})
	// Items completed before processing starts do not contribute processing durations.
	seedBatch2(t, db, seedBatch{id: 6, source: "manual", profile: 1, title: "配置一", status: "completed",
		created: at(base.Add(10 * time.Minute)), started: at(base.Add(30 * time.Minute)), finished: at(base.Add(40 * time.Minute)),
		discovered: 2, done: 2,
		items: []seedItem{{status: "done", finished: base.Add(40 * time.Minute).UnixMilli()}, {status: "done", finished: base.Add(40 * time.Minute).UnixMilli()}}})
}

func TestAnalyticsTotalsRatesAndPercentiles(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	seededDashboard(t, db, base)
	from, to := base.UnixMilli(), base.Add(10*time.Hour).UnixMilli()
	report, err := Analytics(t.Context(), db, AnalysisQuery{From: from, To: to, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	totals := report.Totals
	if totals.Batches != 5 || totals.Completed != 2 || totals.PartialFailure != 1 || totals.FailedBatches != 1 || totals.Cancelled != 1 || totals.Active != 0 {
		t.Fatal("batch statuses", totals)
	}
	if totals.Done != 5 || totals.Skipped != 1 || totals.Failed != 3 {
		t.Fatal("item outcomes", totals)
	}
	if totals.TimedItems != 4 {
		t.Fatal("timing attribution", totals)
	}
	if totals.AvgBatchMs != 1_332_000 { // (3.6M+1.8M+0.6M+0.06M+0.6M)h / 5
		t.Fatal("avg batch duration", totals.AvgBatchMs)
	}
	if totals.AvgItemMs != 20_000 || totals.P50ItemMs != 10_000 || totals.P95ItemMs != 40_000 {
		t.Fatal("item duration stats", totals.AvgItemMs, totals.P50ItemMs, totals.P95ItemMs)
	}
	if totals.ThroughputPerHour != 0.9 { // 9 processed / 10h
		t.Fatal("throughput", totals.ThroughputPerHour)
	}
	if len(report.Scatter) != 5 || report.Scatter[0].Run != 6 {
		t.Fatal("scatter keeps finished batches newest first", report.Scatter)
	}
	if len(report.Options.Profiles) != 3 {
		t.Fatal("options include profiles outside the range", report.Options)
	}
}

func TestAnalyticsTrendDistributionAndFailures(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	seededDashboard(t, db, base)
	report, err := Analytics(t.Context(), db, AnalysisQuery{From: base.UnixMilli(), To: base.Add(10 * time.Hour).UnixMilli(), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Range.Interval != "hour" || len(report.Trend) != 10 {
		t.Fatal("hour buckets over ten hours", report.Range.Interval, len(report.Trend))
	}
	byHour := map[int]*TrendBucket{}
	for i := range report.Trend {
		bucket := &report.Trend[i]
		hour, err := time.Parse(time.RFC3339, bucket.From)
		if err != nil {
			t.Fatal(err)
		}
		byHour[hour.Hour()] = bucket
	}
	first := byHour[9]
	if first.FinishedBatches != 1 || first.AvgBatchMs != 600_000 || first.Done != 2 || first.Skipped != 1 {
		t.Fatal("09:00 bucket (completed items + scan skip)", first)
	}
	second := byHour[10]
	if second.Done != 1 || second.Failed != 1 || second.FinishedBatches != 0 {
		t.Fatal("10:00 bucket (item completions)", second)
	}
	third := byHour[11]
	if third.FinishedBatches != 3 || third.AvgBatchMs != 820_000 || third.Done != 2 || third.Failed != 2 {
		t.Fatal("11:00 bucket (batch finishes)", third)
	}
	fourth := byHour[12]
	if fourth.FinishedBatches != 1 || fourth.AvgBatchMs != 3_600_000 {
		t.Fatal("12:00 bucket (latest finish)", fourth)
	}
	var distTotal int64
	for _, bucket := range report.Distribution {
		distTotal += bucket.Count
	}
	if distTotal != 4 {
		t.Fatal("distribution total", report.Distribution)
	}
	if report.Distribution[1].Count != 2 || report.Distribution[2].Count != 1 || report.Distribution[3].Count != 1 {
		t.Fatal("distribution bands 5–15s/15–30s/30–60s", report.Distribution)
	}
	if len(report.Failures) != 2 || report.Failures[0].Error.Code != "naming_failed" || report.Failures[0].Count != 2 {
		t.Fatal("failure grouping across batches", report.Failures)
	}
	if len(report.BatchFailures) != 1 || report.BatchFailures[0].Error.Code != "dependency_failed" {
		t.Fatal("batch failures", report.BatchFailures)
	}
}

func TestAnalyticsProfilesFiltersAndPaging(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	seededDashboard(t, db, base)
	from, to := base.UnixMilli(), base.Add(10*time.Hour).UnixMilli()
	report, err := Analytics(t.Context(), db, AnalysisQuery{From: from, To: to, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Profiles) != 2 || report.Profiles[0].ID != 1 || report.Profiles[0].Batches != 4 {
		t.Fatal("profile aggregation", report.Profiles)
	}
	var two *ProfilePerf
	for i := range report.Profiles {
		if report.Profiles[i].ID == 2 {
			two = &report.Profiles[i]
		}
	}
	if two == nil || two.Done != 1 || two.Failed != 1 || two.P50ItemMs != 5_000 || two.P95ItemMs != 40_000 {
		t.Fatal("profile two stats", two)
	}
	// Recent sort orders by actual start descending.
	if report.Batches.Total != 5 || report.Batches.Items[0].ID != 4 || report.Batches.Items[0].AvgItemMs != 0 {
		t.Fatal("recent ordering and per-run averages", report.Batches.Items)
	}
	for _, row := range report.Batches.Items {
		if row.ID == 1 && row.AvgItemMs != 17_500 {
			t.Fatal("run average", row)
		}
		if row.ID == 2 && row.AvgItemMs != 22_500 {
			t.Fatal("run average", row)
		}
	}
	report, err = Analytics(t.Context(), db, AnalysisQuery{From: from, To: to, Location: time.UTC, Profile: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 1 || report.Batches.Items[0].Source != "scheduled" {
		t.Fatal("profile filter", report.Totals, report.Batches.Items)
	}
	report, err = Analytics(t.Context(), db, AnalysisQuery{From: from, To: to, Location: time.UTC, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 4 {
		t.Fatal("source filter", report.Totals)
	}
	report, err = Analytics(t.Context(), db, AnalysisQuery{From: from, To: to, Location: time.UTC, Sort: "duration"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Batches.Items[0].ID != 1 || report.Batches.Items[1].ID != 2 {
		t.Fatal("duration sort", report.Batches.Items)
	}
	report, err = Analytics(t.Context(), db, AnalysisQuery{From: from, To: to, Location: time.UTC, Sort: "size"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Batches.Items[0].ID != 1 {
		t.Fatal("size sort", report.Batches.Items)
	}
	report, err = Analytics(t.Context(), db, AnalysisQuery{From: from, To: to, Location: time.UTC, Offset: 3, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Batches.Items) != 2 || report.Batches.Offset != 3 {
		t.Fatal("pagination", report.Batches)
	}
}

func TestAnalyticsAllHistoryStartsAtFirstBatch(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	seededDashboard(t, db, base)
	// Earliest selected batch starts an hour before base; span 11h → hour buckets.
	report, err := Analytics(t.Context(), db, AnalysisQuery{From: 0, To: base.Add(10 * time.Hour).UnixMilli(), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 6 || report.Range.Interval != "hour" || len(report.Trend) != 11 {
		t.Fatal("all-history range", report.Totals, report.Range, len(report.Trend))
	}
}

func TestAnalyticsEmptyRangeKeepsFrame(t *testing.T) {
	db := analyticsDB(t)
	report, err := Analytics(t.Context(), db, AnalysisQuery{From: 0, To: int64(6 * time.Hour / time.Millisecond), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 0 || report.Totals.Done != 0 || len(report.Batches.Items) != 0 {
		t.Fatal("empty totals", report.Totals)
	}
	if report.Range.Interval != "15m" || len(report.Trend) != 24 {
		t.Fatal("trend should still cover the range", report.Range.Interval, len(report.Trend))
	}
	if len(report.Distribution) == 0 || report.Distribution[0].Count != 0 {
		t.Fatal("distribution frame", report.Distribution)
	}
}

func TestQueuedCancellationAndRenamedProfiles(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	seedBatch2(t, db, seedBatch{id: 1, source: "manual", profile: 1, title: "旧名", status: "cancelled", created: at(base), finished: at(base.Add(time.Hour))})
	seedBatch2(t, db, seedBatch{id: 2, source: "manual", profile: 1, title: "新名", status: "completed", created: at(base), started: at(base), finished: at(base.Add(time.Minute))})
	report, err := Analytics(t.Context(), db, AnalysisQuery{To: base.Add(2 * time.Hour).UnixMilli(), Offset: int(^uint(0) >> 1), Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.AvgBatchMs != 60_000 || len(report.Scatter) != 1 {
		t.Fatal("queue time counted as processing", report.Totals, report.Scatter)
	}
	if len(report.Options.Profiles) != 1 || report.Options.Profiles[0].Title != "新名" {
		t.Fatal("profile option duplicated", report.Options)
	}
	if report.Batches.Offset != 0 || len(report.Batches.Items) != 2 {
		t.Fatal("invalid pagination", report.Batches)
	}
}

func TestFilterExtractionWithoutModel(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	for _, b := range []seedBatch{
		{id: 1, task: "extract", source: "manual", status: "completed", done: 1, discovered: 1,
			created: at(base), started: at(base), finished: at(base.Add(5 * time.Second)),
			items: []seedItem{{status: "done", started: base.UnixMilli(), finished: base.Add(5 * time.Second).UnixMilli()}}},
		{id: 2, task: "rename", profile: 1, title: "命名模型", source: "manual", status: "completed", done: 1, discovered: 1,
			created: at(base), started: at(base), finished: at(base.Add(time.Minute)),
			items: []seedItem{{status: "done", started: base.UnixMilli(), finished: base.Add(time.Minute).UnixMilli()}}},
		{id: 3, task: "extract", source: "schedule", plan: 1, status: "running", created: at(base)},
	} {
		seedBatch2(t, db, b)
	}
	report, err := Analytics(t.Context(), db, AnalysisQuery{To: base.Add(time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 3 || report.Totals.Done != 2 || report.Totals.Active != 1 || report.Totals.AvgItemMs != 32_500 || report.Totals.AvgBatchMs != 32_500 {
		t.Fatal("extraction missing from overall totals", report.Totals)
	}
	if len(report.Profiles) != 1 || report.Profiles[0].ID != 1 || report.Profiles[0].Batches != 1 || report.Profiles[0].Done != 1 || report.Profiles[0].AvgItemMs != 60_000 || report.Profiles[0].P50ItemMs != 60_000 || report.Profiles[0].P95ItemMs != 60_000 || report.Profiles[0].ThroughputPerHour != 1 {
		t.Fatal("extraction included in model comparison", report.Profiles)
	}
	if len(report.Options.Profiles) != 2 || report.Options.Profiles[0].ID != 0 {
		t.Fatal("extraction filter missing", report.Options)
	}
	var trendDone, timedItems int64
	for _, b := range report.Trend {
		trendDone += b.Done
	}
	for _, b := range report.Distribution {
		timedItems += b.Count
	}
	if trendDone != 2 || timedItems != 2 || len(report.Scatter) != 2 || report.Scatter[1].Task != "extract" {
		t.Fatal("extraction missing from charts", report.Trend, report.Distribution, report.Scatter)
	}
	report, err = Analytics(t.Context(), db, AnalysisQuery{To: base.Add(time.Hour).UnixMilli(), ProfileSet: true, Profile: 0})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 2 || len(report.Profiles) != 0 || len(report.Batches.Items) != 2 || report.Totals.AvgItemMs != 5_000 {
		t.Fatal("extraction-only report", report)
	}
	for _, b := range report.Batches.Items {
		if b.Task != "extract" || b.ProfileID != 0 || b.ProfileTitle != "" || b.Model != "" {
			t.Fatal("extraction batch metadata", b)
		}
	}
}

func TestAnalyticsCachePagesAndCommittedChanges(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	for i := int64(1); i <= 3; i++ {
		seedBatch2(t, db, seedBatch{id: i, source: "manual", profile: 1, title: "profile", status: "completed", created: at(base.Add(time.Duration(i) * time.Hour)), started: at(base.Add(time.Duration(i) * time.Hour)), finished: at(base.Add(time.Duration(i)*time.Hour + time.Minute)), done: i})
	}
	q := AnalysisQuery{To: base.Add(24 * time.Hour).UnixMilli(), Limit: 1}
	first, err := Analytics(t.Context(), db, q)
	if err != nil || len(first.Batches.Items) != 1 || first.Batches.Items[0].ID != 3 {
		t.Fatal(first, err)
	}
	first.Totals.Done = 999
	q.Offset = 1
	second, err := Analytics(t.Context(), db, q)
	if err != nil || second.Totals.Done != 6 || second.Batches.Items[0].ID != 2 {
		t.Fatal(second, err)
	}
	if _, err = storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE runs SET done=9,model_config_title='renamed' WHERE id=3")
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := Analytics(t.Context(), db, q)
	if err != nil || changed.Totals.Done != 12 || changed.Options.Profiles[0].Title != "renamed" {
		t.Fatal(changed, err)
	}
}
