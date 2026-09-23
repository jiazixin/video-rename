package tokenusage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/storage"
)

func openStore(t *testing.T) *storage.Store {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedRun(t *testing.T, db *storage.Store, id int64, summary string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO runs(id,source,targets,recursive,status,created,request_key,config_summary) VALUES(?, 'manual','[]',1,'completed',?,?,?)`, id, storage.Now(), storage.Now(), summary); err != nil {
		t.Fatal(err)
	}
}

func seedItem(t *testing.T, db *storage.Store, id, run int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO entries(id,parent,name,kind,identity,size,mtime,observed) VALUES(1,0,'clip.mp4','file','i',1,'m','o')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO run_items(id,run,entry,identity,size,mtime) VALUES(?,?,1,?,1,'m')`, id, run, fmt.Sprint("identity-", id)); err != nil {
		t.Fatal(err)
	}
}

func aggregate(t *testing.T, db *storage.Store, run int64) (tokens int64, records int64, estimated int64) {
	t.Helper()
	if err := db.QueryRow("SELECT token_count,token_records,token_estimated_records FROM run_token_usage WHERE run=?", run).Scan(&tokens, &records, &estimated); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRecordWritesLedgerAndAggregateOnce(t *testing.T) {
	db := openStore(t)
	seedRun(t, db, 7, `{"model":"vision","provider":"custom","protocol":"openai"}`)
	seedItem(t, db, 101, 7)
	at := time.Date(2026, 9, 23, 8, 30, 0, 0, time.UTC)
	if err := Record(db, 7, 101, `{"_local_context":{"text_tokens_estimate":75}}`, `{"usage":{"total_tokens":120}}`, at); err != nil {
		t.Fatal(err)
	}
	// Replaying the same item must not advance either side.
	if err := Record(db, 7, 101, `{"_local_context":{"text_tokens_estimate":75}}`, `{"usage":{"total_tokens":120}}`, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tokens, records, estimated := aggregate(t, db, 7)
	if tokens != 120 || records != 1 || estimated != 0 {
		t.Fatal("aggregate mismatch", tokens, records, estimated)
	}
	var ledgerTokens, ledgerRecords, ledgerEstimated, legacy int
	if err := db.QueryRow("SELECT tokens,records,estimated_records,legacy FROM token_usage_records WHERE run=7 AND item=101").Scan(&ledgerTokens, &ledgerRecords, &ledgerEstimated, &legacy); err != nil {
		t.Fatal(err)
	}
	if ledgerTokens != 120 || ledgerRecords != 1 || ledgerEstimated != 0 || legacy != 0 {
		t.Fatal("ledger mismatch", ledgerTokens, ledgerRecords, ledgerEstimated, legacy)
	}
	var recordedAt int64
	if err := db.QueryRow("SELECT recorded_at FROM token_usage_records WHERE item=101").Scan(&recordedAt); err != nil || recordedAt != at.UnixMilli() {
		t.Fatal("recorded time mismatch", recordedAt, err)
	}
}

func TestRecordKeepsEstimatesAndSkipsUncountedExchanges(t *testing.T) {
	db := openStore(t)
	seedRun(t, db, 8, `{}`)
	seedItem(t, db, 102, 8)
	seedItem(t, db, 103, 8)
	if err := Record(db, 8, 102, `{"_local_context":{"text_tokens_estimate":75}}`, `{"error":"timeout"}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	// No usage and no saved estimate: nothing is written anywhere.
	if err := Record(db, 8, 103, `{"messages":[]}`, `{}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	tokens, records, estimated := aggregate(t, db, 8)
	if tokens != 75 || records != 1 || estimated != 1 {
		t.Fatal("aggregate mismatch", tokens, records, estimated)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM token_usage_records WHERE run=8").Scan(&count); err != nil || count != 1 {
		t.Fatal("ledger rows", count, err)
	}
}
