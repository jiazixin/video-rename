package tokenusage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/storage"
)

func openStore(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "usage.sqlite"))
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

type seedRunOptions struct {
	db      *storage.Store
	id      int64
	summary string
}

func seedRun(t *testing.T, options seedRunOptions) {
	t.Helper()
	db, id, summary := options.db, options.id, options.summary

	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,request_key,config_summary) VALUES(?, 'manual','[]',1,'completed',?,?,?)`, id, storage.Now(), storage.Now(), summary)
	}); err != nil {
		t.Fatal(err)
	}
}

type seedItemOptions struct {
	db  *storage.Store
	id  int64
	run int64
}

func seedItem(t *testing.T, options seedItemOptions) {
	t.Helper()
	db, id, run := options.db, options.id, options.run

	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT OR IGNORE INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(1,0,'clip.mp4','file','i',1,0,'o')`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO run_items(id,run,entry,identity,size,mtime_ns) VALUES(?,?,1,?,1,0)`, id, run, fmt.Sprint("identity-", id))
	}); err != nil {
		t.Fatal(err)
	}
}

func aggregate(t *testing.T, db *storage.Store, run int64) (tokens int64, records int64, estimated int64) {
	t.Helper()
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT token_count,token_records,token_estimated_records FROM run_token_usage WHERE run=?", run).Scan(&tokens, &records, &estimated)
	}); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRecordWritesLedgerAndAggregateOnce(t *testing.T) {
	db := openStore(t)
	seedRun(t, seedRunOptions{db: db, id: 7, summary: `{"model":"vision","provider":"custom","protocol":"openai"}`})
	seedItem(t, seedItemOptions{db: db, id: 101, run: 7})
	at := time.Date(2026, 9, 23, 8, 30, 0, 0, time.UTC)
	if err := RecordAttempt(t.Context(), RecordAttemptOptions{Store: db, Run: 7, Item: 101, Attempt: 1, ProfileID: 1, Title: "测试模型", Summary: `{}`, Request: `{"_local_context":{"text_tokens_estimate":75}}`, Response: `{"usage":{"total_tokens":120}}`, At: at}); err != nil {
		t.Fatal(err)
	}
	// Replaying the same item must not advance either side.
	if err := RecordAttempt(t.Context(), RecordAttemptOptions{Store: db, Run: 7, Item: 101, Attempt: 1, ProfileID: 1, Title: "测试模型", Summary: `{}`, Request: `{"_local_context":{"text_tokens_estimate":75}}`, Response: `{"usage":{"total_tokens":120}}`, At: at.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	tokens, records, estimated := aggregate(t, db, 7)
	if tokens != 120 || records != 1 || estimated != 0 {
		t.Fatal("aggregate mismatch", tokens, records, estimated)
	}
	var ledgerTokens, ledgerRecords, ledgerEstimated int
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT tokens,records,estimated_records FROM token_usage_records WHERE run=7 AND item=101").Scan(&ledgerTokens, &ledgerRecords, &ledgerEstimated)
	}); err != nil {
		t.Fatal(err)
	}
	if ledgerTokens != 120 || ledgerRecords != 1 || ledgerEstimated != 0 {
		t.Fatal("ledger mismatch", ledgerTokens, ledgerRecords, ledgerEstimated)
	}
	var recordedAt int64
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT recorded_at FROM token_usage_records WHERE item=101").Scan(&recordedAt)
	}); err != nil || recordedAt != at.UnixMilli() {
		t.Fatal("recorded time mismatch", recordedAt, err)
	}
}

func TestRecordKeepsEstimatesAndSkipsUncountedExchanges(t *testing.T) {
	db := openStore(t)
	seedRun(t, seedRunOptions{db: db, id: 8, summary: `{}`})
	seedItem(t, seedItemOptions{db: db, id: 102, run: 8})
	seedItem(t, seedItemOptions{db: db, id: 103, run: 8})
	if err := RecordAttempt(t.Context(), RecordAttemptOptions{Store: db, Run: 8, Item: 102, Attempt: 1, ProfileID: 1, Title: "测试模型", Summary: `{}`, Request: `{"_local_context":{"text_tokens_estimate":75}}`, Response: `{"error":"timeout"}`, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// No usage and no saved estimate: nothing is written anywhere.
	if err := RecordAttempt(t.Context(), RecordAttemptOptions{Store: db, Run: 8, Item: 103, Attempt: 1, ProfileID: 1, Title: "测试模型", Summary: `{}`, Request: `{"messages":[]}`, Response: `{}`, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tokens, records, estimated := aggregate(t, db, 8)
	if tokens != 75 || records != 1 || estimated != 1 {
		t.Fatal("aggregate mismatch", tokens, records, estimated)
	}
	var count int
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT count(*) FROM token_usage_records WHERE run=8").Scan(&count)
	}); err != nil || count != 1 {
		t.Fatal("ledger rows", count, err)
	}
}

func TestAttemptsCountOnceAndGroupByActualModel(t *testing.T) {
	db := openStore(t)
	seedRun(t, seedRunOptions{db: db, id: 9, summary: `{"model":"primary"}`})
	seedItem(t, seedItemOptions{db: db, id: 104, run: 9})
	at := time.Now()
	for _, n := range []int{1, 2, 2} {
		profile, title, summary := int64(1), "主模型", `{"model":"primary"}`
		if n == 2 {
			profile, title, summary = 2, "兜底", `{"model":"fallback"}`
		}
		if err := RecordAttempt(t.Context(), RecordAttemptOptions{Store: db, Run: 9, Item: 104, Attempt: n, ProfileID: profile, Title: title, Summary: summary, Request: `{}`, Response: `{"usage":{"total_tokens":10}}`, At: at}); err != nil {
			t.Fatal(err)
		}
	}
	tokens, records, _ := aggregate(t, db, 9)
	if tokens != 20 || records != 2 {
		t.Fatal(tokens, records)
	}
	report, err := Analytics(t.Context(), db, AnalyticsQuery{From: at.Add(-time.Minute).UnixMilli(), To: at.Add(time.Minute).UnixMilli(), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Models) != 2 || len(report.Profiles) != 2 {
		t.Fatal(report.Models, report.Profiles)
	}
	for _, model := range report.Models {
		if model.Tokens != 10 || model.Batches != 1 {
			t.Fatal(model)
		}
	}
	report, err = Analytics(t.Context(), db, AnalyticsQuery{From: at.Add(-time.Minute).UnixMilli(), To: at.Add(time.Minute).UnixMilli(), Location: time.UTC, Profile: 2})
	if err != nil || report.Totals.Tokens != 10 {
		t.Fatal(report, err)
	}
}
