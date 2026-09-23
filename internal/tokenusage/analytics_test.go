package tokenusage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/storage"
)

func analyticsDB(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "analytics.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedUsage(t *testing.T, db *storage.Store, run int64, source string, plan int64, summary string, profileID int64, title string, rows [][4]int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO runs(id,source,targets,recursive,status,created,started,finished,request_key,plan,config_summary,model_config_id,model_config_title) VALUES(?,?,'[]',1,'completed','2026-09-01T00:00:00Z','2026-09-01T00:00:00Z','2026-09-01T01:00:00Z',?,?,?,?,?)`, run, source, storage.Now(), plan, summary, profileID, title); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO entries(id,parent,name,kind,identity,size,mtime,observed) VALUES(1,0,'clip.mp4','file','i',1,'m','o')`); err != nil {
		t.Fatal(err)
	}
	var lifetime, estimatedCount int64
	for i, row := range rows {
		// row is recorded_at(ms), tokens, estimated(0/1), legacy(0/1); records is 1.
		item := run*1000 + int64(i)
		if _, err := db.Exec(`INSERT INTO run_items(id,run,entry,identity,size,mtime) VALUES(?,?,1,?,1,'m')`, item, run, fmt.Sprint("identity-", item)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO token_usage_records(run,item,recorded_at,tokens,records,estimated_records,legacy) VALUES(?,?,?,?,1,?,?)`, run, item, row[0], row[1], row[2], row[3]); err != nil {
			t.Fatal(err)
		}
		lifetime += row[1]
		estimatedCount += row[2]
	}
	if _, err := db.Exec(`INSERT INTO run_token_usage(run,token_count,token_records,token_estimated_records) VALUES(?,?,?,?)`, run, lifetime, int64(len(rows)), estimatedCount); err != nil {
		t.Fatal(err)
	}
}

var hour = int64(time.Hour / time.Millisecond)

func TestAnalyticsHalfOpenRangeAndLegacySplit(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	seedUsage(t, db, 1, "manual", 0, `{"model":"vision-a","provider":"custom","protocol":"openai"}`, 1, "配置一", [][4]int64{
		{base.UnixMilli() + 2*hour, 100, 0, 0},
	})
	seedUsage(t, db, 2, "schedule", 3, `{"model":"vision-b","provider":"zhipu","protocol":"openai"}`, 2, "配置二", [][4]int64{
		{base.UnixMilli() + 3*hour, 40, 1, 1},
	})
	from := base.Add(2 * time.Hour).UnixMilli()
	to := base.Add(3 * time.Hour).UnixMilli() // exactly on the record: excluded
	report, err := Analytics(db, AnalyticsQuery{From: from, To: to, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 100 || report.Totals.Records != 1 || report.Totals.LegacyRecords != 0 {
		t.Fatal("range totals", report.Totals)
	}
	if len(report.Models) != 1 || report.Models[0].Model != "vision-a" {
		t.Fatal("models", report.Models)
	}
	if report.Totals.Batches != 1 || len(report.Batches.Items) != 1 || report.Batches.Items[0].ID != 1 {
		t.Fatal("batches", report.Batches)
	}
	if got := report.Batches.Items[0].LifetimeTokens; got != 100 {
		t.Fatal("lifetime tokens", got)
	}
	// Widen the range: the scheduled legacy row joins and stays marked legacy.
	report, err = Analytics(db, AnalyticsQuery{From: from, To: base.Add(4 * time.Hour).UnixMilli(), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 140 || report.Totals.Records != 2 || report.Totals.EstimatedRecords != 1 || report.Totals.LegacyRecords != 1 {
		t.Fatal("widened totals", report.Totals)
	}
	if len(report.Sources) != 2 {
		t.Fatal("sources", report.Sources)
	}
	for _, group := range report.Sources {
		if group.Key == "scheduled" && (group.Tokens != 40 || group.LegacyRecords != 1) {
			t.Fatal("scheduled group", group)
		}
	}
}

func TestAnalyticsFiltersAndInvariants(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	seedUsage(t, db, 1, "manual", 0, `{"model":"a","provider":"p1","protocol":"openai"}`, 1, "一", [][4]int64{{base.UnixMilli(), 10, 0, 0}})
	seedUsage(t, db, 2, "manual", 0, `{"model":"b","provider":"p2","protocol":"openai"}`, 1, "一", [][4]int64{{base.UnixMilli(), 20, 0, 0}})
	seedUsage(t, db, 3, "schedule", 1, `{"model":"a","provider":"p1","protocol":"openai"}`, 2, "二", [][4]int64{{base.UnixMilli(), 30, 1, 0}})
	report, err := Analytics(db, AnalyticsQuery{From: 0, To: base.Add(24 * time.Hour).UnixMilli(), Location: time.UTC, Model: modelKey("p1", "openai", "a")})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 40 || len(report.Models) != 1 || report.Totals.Batches != 2 {
		t.Fatal("model filter", report.Totals, report.Models)
	}
	var trendTokens, modelTokens, sourceTokens int64
	for _, bucket := range report.Trend {
		trendTokens += bucket.Tokens
	}
	for _, model := range report.Models {
		modelTokens += model.Tokens
	}
	for _, source := range report.Sources {
		sourceTokens += source.Tokens
	}
	if trendTokens != 40 || modelTokens != 40 || sourceTokens != 40 {
		t.Fatal("dimensions disagree", trendTokens, modelTokens, sourceTokens)
	}
	report, err = Analytics(db, AnalyticsQuery{From: 0, To: base.Add(24 * time.Hour).UnixMilli(), Location: time.UTC, Profile: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 30 || len(report.Batches.Items) != 1 || report.Batches.Items[0].Source != "scheduled" {
		t.Fatal("profile filter", report.Totals, report.Batches.Items)
	}
	report, err = Analytics(db, AnalyticsQuery{From: 0, To: base.Add(24 * time.Hour).UnixMilli(), Location: time.UTC, Run: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 20 {
		t.Fatal("run filter", report.Totals)
	}
	// Options list every recorded identity even when filters hide them.
	if len(report.Options.Models) != 2 || len(report.Options.Profiles) != 2 {
		t.Fatal("options", report.Options)
	}
}

func TestAnalyticsPaginationSortingAndDayBoundaries(t *testing.T) {
	db := analyticsDB(t)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	// 06:00 local on 2026-09-23 in Shanghai = 22:00 UTC on 09-22; the local
	// day bucket must start at local midnight, not UTC midnight.
	local := time.Date(2026, 9, 23, 6, 0, 0, 0, shanghai)
	for i := int64(1); i <= 60; i++ {
		tokens := i * 10
		seedUsage(t, db, i, "manual", 0, `{"model":"m","provider":"p","protocol":"openai"}`, 1, "一", [][4]int64{{local.UnixMilli(), tokens, 0, 0}})
	}
	// A seven-day window picks day buckets aligned to Shanghai midnights.
	report, err := Analytics(db, AnalyticsQuery{From: time.Date(2026, 9, 20, 0, 0, 0, 0, shanghai).UnixMilli(), To: time.Date(2026, 9, 27, 0, 0, 0, 0, shanghai).UnixMilli(), Location: shanghai, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if report.Range.Interval != "day" || len(report.Trend) != 7 {
		t.Fatal("day interval and buckets", report.Range.Interval, len(report.Trend))
	}
	dayStart, _ := time.Parse(time.RFC3339, report.Trend[3].From)
	_, offset := dayStart.Zone()
	if dayStart.Hour() != 0 || dayStart.Day() != 23 || offset != 8*60*60 {
		t.Fatal("day bucket should start at local midnight", dayStart)
	}
	if report.Trend[3].Tokens != 18300 {
		t.Fatal("day bucket totals", report.Trend[3].Tokens)
	}
	if report.Batches.Total != 60 || len(report.Batches.Items) != 20 || report.Batches.Items[0].ID != 60 {
		t.Fatal("token sort should rank the largest first", report.Batches.Total, report.Batches.Items[0].ID)
	}
	if len(report.TopBatches) != 10 || report.TopBatches[0].ID != 60 {
		t.Fatal("top batches", len(report.TopBatches), report.TopBatches[0].ID)
	}
	// All history starts at the first recorded usage and follows its extent.
	report, err = Analytics(db, AnalyticsQuery{From: 0, To: time.Date(2026, 9, 24, 6, 0, 0, 0, shanghai).UnixMilli(), Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if report.Range.Interval != "hour" || len(report.Trend) != 24 {
		t.Fatal("all-history interval", report.Range.Interval, len(report.Trend))
	}
	report, err = Analytics(db, AnalyticsQuery{From: 0, To: time.Date(2026, 9, 27, 0, 0, 0, 0, shanghai).UnixMilli(), Location: shanghai, Sort: "recent", Page: 3, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	// Equal timestamps tie-break by id descending, so page 3 of recent starts at #20.
	if len(report.Batches.Items) != 20 || report.Batches.Items[0].ID != 20 {
		t.Fatal("recent sort and pagination", report.Batches.Items[0].ID)
	}
}

func TestAnalyticsEmptyRangeKeepsFrame(t *testing.T) {
	db := analyticsDB(t)
	report, err := Analytics(db, AnalyticsQuery{From: 0, To: 6 * hour, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 0 || report.Totals.Records != 0 || report.Totals.Batches != 0 {
		t.Fatal("empty totals", report.Totals)
	}
	if len(report.Trend) != 24 || report.Trend[0].Tokens != 0 {
		t.Fatal("trend should still cover the range", len(report.Trend))
	}
	if report.Range.Interval != "15m" {
		t.Fatal("interval", report.Range.Interval)
	}
	if len(report.Batches.Items) != 0 || report.Batches.Total != 0 {
		t.Fatal("batches on empty", report.Batches)
	}
}
