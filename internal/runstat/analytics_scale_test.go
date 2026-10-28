//go:build scale

package runstat

import (
	"database/sql"
	"testing"
	"time"

	"video-rename/internal/tokenusage"
)

func TestScaleAnalyticsFilteringAndCachedPages(t *testing.T) {
	db := analyticsDB(t)
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		queries := []string{
			`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000)
   INSERT INTO runs(id,source,targets,recursive,status,created,started,finished,request_key,done,discovered,config_summary,model_config_id)
   SELECT x,'manual','[]',0,'completed',strftime('%Y-%m-%dT%H:%M:%fZ',x*3600,'unixepoch'),strftime('%Y-%m-%dT%H:%M:%fZ',x*3600,'unixepoch'),strftime('%Y-%m-%dT%H:%M:%fZ',x*3600+60,'unixepoch'),'scale-'||x,100,100,'{"model":"vision","provider":"custom","protocol":"openai"}',1 FROM n`,
			`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000)
   INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) SELECT x,0,'clip-'||x||'.mp4','file','file-'||x,1,0,'o' FROM n`,
			`INSERT INTO run_items(id,run,entry,identity,size,mtime_ns,status,message,started_ms,finished_ms)
   SELECT id,(id-1)/100+1,id,identity,1,0,'done',printf('%.*c',1024,'x'),((id-1)/100+1)*3600000,((id-1)/100+1)*3600000+1000 FROM entries`,
			`INSERT INTO token_usage_records(run,item,recorded_at,tokens) SELECT run,id,finished_ms,10 FROM run_items`,
			`INSERT INTO run_token_usage(run,token_count,token_records) SELECT run,sum(tokens),count(*) FROM token_usage_records GROUP BY run`,
		}
		for _, query := range queries {
			if _, err = tx.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}
	q := AnalysisQuery{To: 1001 * 3600000, Size: 20}
	for _, page := range []int{1, 2} {
		q.Page = page
		start := time.Now()
		r, err := Analytics(t.Context(), db, q)
		if err != nil || r.Totals.TimedItems != 100000 || r.Batches.Page != page {
			t.Fatal(r, err)
		}
		t.Logf("run analytics page %d: %s", page, time.Since(start))
	}
	q.From = 1000 * 3600000
	q.Page = 1
	start := time.Now()
	r, err := Analytics(t.Context(), db, q)
	if err != nil || r.Totals.Batches != 1 || r.Totals.TimedItems != 100 {
		t.Fatal(r, err)
	}
	t.Logf("run analytics one indexed hour: %s", time.Since(start))
	u := tokenusage.AnalyticsQuery{To: q.To, Size: 20}
	for _, page := range []int{1, 2} {
		u.Page = page
		start = time.Now()
		r, err := tokenusage.Analytics(t.Context(), db, u)
		if err != nil || r.Totals.Tokens != 1000000 || r.Batches.Page != page {
			t.Fatal(r, err)
		}
		t.Logf("usage analytics page %d: %s", page, time.Since(start))
	}
	u.Run = 1000
	u.Page = 1
	start = time.Now()
	usage, err := tokenusage.Analytics(t.Context(), db, u)
	if err != nil || usage.Totals.Tokens != 1000 || usage.Totals.Batches != 1 {
		t.Fatal(usage, err)
	}
	t.Logf("usage analytics one run: %s", time.Since(start))
}
