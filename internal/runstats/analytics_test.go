package runstats

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/storage"
)

func analyticsDB(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "runstats.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
	plan                               int64
	profile                            int64
	title                              string
	status, created, started, finished string
	discovered, done, skipped, failed  int64
	err                                string
	items                              []seedItem
}

func at(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func seedBatch2(t *testing.T, db *storage.Store, b seedBatch) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO runs(id,source,targets,recursive,status,created,started,finished,discovered,done,skipped,failed,error,request_key,plan,config_summary,model_config_id,model_config_title) VALUES(?,?,'[]',1,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.id, b.source, b.status, b.created, b.started, b.finished, b.discovered, b.done, b.skipped, b.failed, b.err, fmt.Sprint("key-", b.id), b.plan, `{"model":"vision","provider":"custom","protocol":"openai"}`, b.profile, b.title); err != nil {
		t.Fatal(err)
	}
	for i, item := range b.items {
		entry := b.id*1000 + int64(i) + 1
		if _, err := db.Exec(`INSERT INTO entries(id,parent,name,kind,identity,size,mtime,observed) VALUES(?,1,?,'file',?,1,'m','o')`, entry, fmt.Sprint("name-", entry), fmt.Sprint("identity-", entry)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO run_items(id,run,entry,identity,size,mtime,status,message,started_ms,finished_ms) VALUES(?,?,?,?,1,'m',?,?,?,?)`,
			b.id*100+int64(i)+1, b.id, entry, fmt.Sprint("identity-", entry), item.status, item.message, item.started, item.finished); err != nil {
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
			{status: "failed", message: "模型返回命名失败\n详细堆栈", started: base.Add(105 * time.Minute).UnixMilli(), finished: base.Add(105*time.Minute + 5*time.Second).UnixMilli()},
		}})
	seedBatch2(t, db, seedBatch{id: 3, source: "manual", profile: 1, title: "配置一", status: "cancelled",
		created: at(base.Add(time.Hour)), started: at(base.Add(130 * time.Minute)), finished: at(base.Add(140 * time.Minute)),
		discovered: 1, failed: 1,
		items: []seedItem{{status: "failed", message: "取消前失败", finished: base.Add(140 * time.Minute).UnixMilli()}}})
	seedBatch2(t, db, seedBatch{id: 4, source: "manual", profile: 1, title: "配置一", status: "failed", err: "全局依赖故障",
		created: at(base.Add(time.Hour)), started: at(base.Add(150 * time.Minute)), finished: at(base.Add(151 * time.Minute)),
		discovered: 1, failed: 1,
		items: []seedItem{{status: "failed", message: "模型返回命名失败", finished: base.Add(151 * time.Minute).UnixMilli()}}})
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
	report, err := Analytics(db, AnalysisQuery{From: from, To: to, Location: time.UTC})
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
	report, err := Analytics(db, AnalysisQuery{From: base.UnixMilli(), To: base.Add(10 * time.Hour).UnixMilli(), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Range.Interval != "hour" || len(report.Trend) != 10 {
		t.Fatal("hour buckets over ten hours", report.Range.Interval, len(report.Trend))
	}
	byHour := map[int]*TrendBucket{}
	for i := range report.Trend {
		bucket := &report.Trend[i]
		hour, _ := time.Parse(time.RFC3339, bucket.From)
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
	if len(report.Failures) != 2 || report.Failures[0].Message != "模型返回命名失败" || report.Failures[0].Count != 2 {
		t.Fatal("failure grouping across batches", report.Failures)
	}
	if len(report.BatchFailures) != 1 || report.BatchFailures[0].Message != "全局依赖故障" {
		t.Fatal("batch failures", report.BatchFailures)
	}
}

func TestAnalyticsProfilesFiltersAndPaging(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	seededDashboard(t, db, base)
	from, to := base.UnixMilli(), base.Add(10*time.Hour).UnixMilli()
	report, err := Analytics(db, AnalysisQuery{From: from, To: to, Location: time.UTC})
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
	report, err = Analytics(db, AnalysisQuery{From: from, To: to, Location: time.UTC, Profile: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 1 || report.Batches.Items[0].Source != "scheduled" {
		t.Fatal("profile filter", report.Totals, report.Batches.Items)
	}
	report, err = Analytics(db, AnalysisQuery{From: from, To: to, Location: time.UTC, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 4 {
		t.Fatal("source filter", report.Totals)
	}
	report, err = Analytics(db, AnalysisQuery{From: from, To: to, Location: time.UTC, Sort: "duration"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Batches.Items[0].ID != 1 || report.Batches.Items[1].ID != 2 {
		t.Fatal("duration sort", report.Batches.Items)
	}
	report, err = Analytics(db, AnalysisQuery{From: from, To: to, Location: time.UTC, Sort: "size"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Batches.Items[0].ID != 1 {
		t.Fatal("size sort", report.Batches.Items)
	}
	report, err = Analytics(db, AnalysisQuery{From: from, To: to, Location: time.UTC, Page: 2, Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Batches.Items) != 2 || report.Batches.Page != 2 {
		t.Fatal("pagination", report.Batches)
	}
}

func TestAnalyticsAllHistoryStartsAtFirstBatch(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	seededDashboard(t, db, base)
	// Earliest selected batch starts an hour before base; span 11h → hour buckets.
	report, err := Analytics(db, AnalysisQuery{From: 0, To: base.Add(10 * time.Hour).UnixMilli(), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 6 || report.Range.Interval != "hour" || len(report.Trend) != 11 {
		t.Fatal("all-history range", report.Totals, report.Range, len(report.Trend))
	}
}

func TestAnalyticsEmptyRangeKeepsFrame(t *testing.T) {
	db := analyticsDB(t)
	report, err := Analytics(db, AnalysisQuery{From: 0, To: int64(6 * time.Hour / time.Millisecond), Location: time.UTC})
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
	report, err := Analytics(db, AnalysisQuery{To: base.Add(2 * time.Hour).UnixMilli(), Page: int(^uint(0) >> 1), Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.AvgBatchMs != 60_000 || len(report.Scatter) != 1 {
		t.Fatal("queue time counted as processing", report.Totals, report.Scatter)
	}
	if len(report.Options.Profiles) != 1 || report.Options.Profiles[0].Title != "新名" {
		t.Fatal("profile option duplicated", report.Options)
	}
	if report.Batches.Page != 1 || len(report.Batches.Items) != 2 {
		t.Fatal("invalid pagination", report.Batches)
	}
}

func TestFilterExtractionWithoutModel(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	for _, id := range []int64{1, 2} {
		seedBatch2(t, db, seedBatch{id: id, profile: id - 1, source: "manual", status: "queued", created: at(base)})
	}
	report, err := Analytics(db, AnalysisQuery{To: base.Add(time.Hour).UnixMilli(), ProfileSet: true, Profile: 0})
	if err != nil || report.Totals.Batches != 1 || report.Batches.Items[0].ProfileID != 0 {
		t.Fatal(report, err)
	}
}
