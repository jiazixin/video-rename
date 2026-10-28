package tokenusage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"video-rename/internal/storage"
)

func analyticsDB(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "analytics.sqlite"))
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

func seedUsage(t *testing.T, db *storage.Store, run int64, source string, plan int64, summary string, profileID int64, title string, rows [][3]int64) {
	t.Helper()
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,started,finished,request_key,plan,config_summary,model_config_id,model_config_title) VALUES(?,?,'[]',1,'completed','2026-09-01T00:00:00.000Z','2026-09-01T00:00:00.000Z','2026-09-01T01:00:00.000Z',?,?,?,?,?)`, run, source, fmt.Sprintf("usage-%d", run), plan, summary, profileID, title)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT OR IGNORE INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(1,0,'clip.mp4','file','i',1,0,'o')`)
	}); err != nil {
		t.Fatal(err)
	}
	var lifetime, estimatedCount int64
	for i, row := range rows {
		// row is recorded_at(ms), tokens, estimated(0/1); records is 1.
		item := run*1000 + int64(i)
		if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), `INSERT INTO run_items(id,run,entry,identity,size,mtime_ns) VALUES(?,?,1,?,1,0)`, item, run, fmt.Sprint("identity-", item))
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), `INSERT INTO token_usage_records(run,item,recorded_at,tokens,records,estimated_records) VALUES(?,?,?,?,1,?)`, run, item, row[0], row[1], row[2])
		}); err != nil {
			t.Fatal(err)
		}
		lifetime += row[1]
		estimatedCount += row[2]
	}
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO run_token_usage(run,token_count,token_records,token_estimated_records) VALUES(?,?,?,?)`, run, lifetime, int64(len(rows)), estimatedCount)
	}); err != nil {
		t.Fatal(err)
	}
}

var hour = int64(time.Hour / time.Millisecond)

func TestAnalyticsHalfOpenRangeAndEstimates(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	seedUsage(t, db, 1, "manual", 0, `{"model":"vision-a","provider":"custom","protocol":"openai"}`, 1, "配置一", [][3]int64{
		{base.UnixMilli() + 2*hour, 100, 0},
	})
	seedUsage(t, db, 2, "schedule", 3, `{"model":"vision-b","provider":"zhipu","protocol":"openai"}`, 2, "配置二", [][3]int64{
		{base.UnixMilli() + 3*hour, 40, 1},
	})
	from := base.Add(2 * time.Hour).UnixMilli()
	to := base.Add(3 * time.Hour).UnixMilli() // exactly on the record: excluded
	report, err := Analytics(t.Context(), db, AnalyticsQuery{From: from, To: to, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 100 || report.Totals.Records != 1 {
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
	// Widen the range: the scheduled estimate joins and stays marked estimated.
	report, err = Analytics(t.Context(), db, AnalyticsQuery{From: from, To: base.Add(4 * time.Hour).UnixMilli(), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 140 || report.Totals.Records != 2 || report.Totals.EstimatedRecords != 1 {
		t.Fatal("widened totals", report.Totals)
	}
	if len(report.Sources) != 2 {
		t.Fatal("sources", report.Sources)
	}
	for _, group := range report.Sources {
		if group.Key == "scheduled" && (group.Tokens != 40 || group.EstimatedRecords != 1) {
			t.Fatal("scheduled group", group)
		}
	}
}

func TestAnalyticsFiltersAndInvariants(t *testing.T) {
	db := analyticsDB(t)
	base := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	seedUsage(t, db, 1, "manual", 0, `{"model":"a","provider":"p1","protocol":"openai"}`, 1, "一", [][3]int64{{base.UnixMilli(), 10, 0}})
	seedUsage(t, db, 2, "manual", 0, `{"model":"b","provider":"p2","protocol":"openai"}`, 1, "一", [][3]int64{{base.UnixMilli(), 20, 0}})
	seedUsage(t, db, 3, "schedule", 1, `{"model":"a","provider":"p1","protocol":"openai"}`, 2, "二", [][3]int64{{base.UnixMilli(), 30, 1}})
	report, err := Analytics(t.Context(), db, AnalyticsQuery{From: 0, To: base.Add(24 * time.Hour).UnixMilli(), Location: time.UTC, Model: modelKey("p1", "openai", "a")})
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
	report, err = Analytics(t.Context(), db, AnalyticsQuery{From: 0, To: base.Add(24 * time.Hour).UnixMilli(), Location: time.UTC, Profile: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 30 || len(report.Batches.Items) != 1 || report.Batches.Items[0].Source != "scheduled" {
		t.Fatal("profile filter", report.Totals, report.Batches.Items)
	}
	report, err = Analytics(t.Context(), db, AnalyticsQuery{From: 0, To: base.Add(24 * time.Hour).UnixMilli(), Location: time.UTC, Run: 2})
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
		seedUsage(t, db, i, "manual", 0, `{"model":"m","provider":"p","protocol":"openai"}`, 1, "一", [][3]int64{{local.UnixMilli(), tokens, 0}})
	}
	// A seven-day window picks day buckets aligned to Shanghai midnights.
	report, err := Analytics(t.Context(), db, AnalyticsQuery{From: time.Date(2026, 9, 20, 0, 0, 0, 0, shanghai).UnixMilli(), To: time.Date(2026, 9, 27, 0, 0, 0, 0, shanghai).UnixMilli(), Location: shanghai, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if report.Range.Interval != "day" || len(report.Trend) != 7 {
		t.Fatal("day interval and buckets", report.Range.Interval, len(report.Trend))
	}
	if got, want := report.Trend[3].From, "2026-09-22T16:00:00.000Z"; got != want {
		t.Fatalf("got day boundary %q, want UTC boundary %q", got, want)
	}
	dayStart, err := time.Parse(time.RFC3339, report.Trend[3].From)
	if err != nil {
		t.Fatal(err)
	}
	dayStart = dayStart.In(shanghai)
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
	report, err = Analytics(t.Context(), db, AnalyticsQuery{From: 0, To: time.Date(2026, 9, 24, 6, 0, 0, 0, shanghai).UnixMilli(), Location: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if report.Range.Interval != "hour" || len(report.Trend) != 24 {
		t.Fatal("all-history interval", report.Range.Interval, len(report.Trend))
	}
	report, err = Analytics(t.Context(), db, AnalyticsQuery{From: 0, To: time.Date(2026, 9, 27, 0, 0, 0, 0, shanghai).UnixMilli(), Location: shanghai, Sort: "recent", Page: 3, Size: 20})
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
	report, err := Analytics(t.Context(), db, AnalyticsQuery{From: 0, To: 6 * hour, Location: time.UTC})
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

func TestTopUsageIndependentOfPageSortAndHugePage(t *testing.T) {
	db := analyticsDB(t)
	seedUsage(t, db, 1, "manual", 0, `{}`, 1, "高消耗", [][3]int64{{1000, 900, 0}})
	seedUsage(t, db, 2, "manual", 0, `{}`, 1, "较新", [][3]int64{{2000, 10, 0}})
	report, err := Analytics(t.Context(), db, AnalyticsQuery{To: 3000, Sort: "recent", Page: int(^uint(0) >> 1), Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if report.TopBatches[0].ID != 1 {
		t.Fatal("ranking followed detail sort", report.TopBatches)
	}
	if report.Batches.Page != 1 || report.Batches.Items[0].ID != 2 {
		t.Fatal("invalid detail pagination", report.Batches)
	}
}

func TestAnalyticsOptionsKeepConfigIdentityAndModelHistory(t *testing.T) {
	db := analyticsDB(t)
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO prompts(id,title,body,created,updated) VALUES(1,'提示词','内容','c','u')`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO model_configs(id,title,prompt_id,created,updated) VALUES(10,'共享配置',1,'c','u')`)
	}); err != nil {
		t.Fatal(err)
	}
	a := `{"model":"a","provider":"p","protocol":"openai"}`
	b := `{"model":"b","provider":"p","protocol":"openai"}`
	seedUsage(t, db, 1, "manual", 0, a, 10, "旧名称", [][3]int64{{1000, 100, 0}})
	seedUsage(t, db, 2, "manual", 0, a, 10, "共享配置", [][3]int64{{2000, 200, 0}})
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `DELETE FROM model_configs WHERE id=10`)
	}); err != nil {
		t.Fatal(err)
	}
	result, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO model_configs(title,prompt_id,created,updated) VALUES('共享配置',1,'c','u')`)
	})
	if err != nil {
		t.Fatal(err)
	}
	newID, err := result.LastInsertId()
	if err != nil || newID == 10 {
		t.Fatal("recreated config must have a new identity", newID, err)
	}
	seedUsage(t, db, 3, "manual", 0, b, newID, "共享配置", [][3]int64{{3000, 300, 0}})
	// The same configuration may have used multiple models and names over time.
	seedUsage(t, db, 4, "manual", 0, a, newID, "改名前", [][3]int64{{1500, 400, 0}})
	// Attempt-level identity may differ from the batch's primary config.
	seedUsage(t, db, 5, "manual", 0, b, newID, "共享配置", [][3]int64{{3500, 500, 0}})
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE token_usage_records SET model_config_id=10,model_config_title='共享配置',config_summary=? WHERE run=5`, b)
	}); err != nil {
		t.Fatal(err)
	}
	report, err := Analytics(t.Context(), db, AnalyticsQuery{From: 2999, To: 4000, Model: modelKey("p", "openai", "b"), Profile: newID})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 300 || len(report.Options.Profiles) != 2 {
		t.Fatal("identities must not merge despite sharing a title", report.Totals, report.Options)
	}
	for _, option := range report.Options.Profiles {
		if option.Title != "共享配置" || !slices.Equal(option.ModelKeys, []string{modelKey("p", "openai", "a"), modelKey("p", "openai", "b")}) {
			t.Fatal("options must use current/latest names and all recorded models", option)
		}
		if option.Deleted != (option.ID == 10) {
			t.Fatal("only the original config was deleted", option)
		}
	}
	// An empty time range must not erase the history used to link filters.
	report, err = Analytics(t.Context(), db, AnalyticsQuery{From: 9000, To: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Records != 0 || len(report.Options.Profiles) != 2 || len(report.Options.Models) != 2 {
		t.Fatal("empty range lost historical filter choices", report.Totals, report.Options)
	}
}

func TestLifetimeUsageBeyondSQLiteParameterLimit(t *testing.T) {
	db := analyticsDB(t)
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		ids := make(map[int64]bool, 40000)
		for id := int64(1); id <= 40000; id++ {
			ids[id] = true
		}
		got, err := lifetimeUsage(t.Context(), tx, ids)
		if err != nil || len(got) != 0 {
			t.Fatal(len(got), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
