package app

import (
	"encoding/json/v2"
	"net/http/httptest"
	"testing"
	"time"

	"video-rename/internal/config"
)

func usageApp(t *testing.T) *App {
	t.Helper()
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	base := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	if _, err := a.DB.Exec(`INSERT INTO entries(id,parent,name,kind,identity,size,mtime,observed) VALUES(500,1,'clip.mp4','file','i',1,'m','o')`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO runs(id,source,targets,recursive,status,created,request_key,config_summary) VALUES(1,'manual','[]',1,'completed','2026-09-01T00:00:00Z','k','{"model":"vision","provider":"custom","protocol":"openai"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO run_items(id,run,entry,identity,size,mtime) VALUES(1,1,500,'i',1,'m')`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO token_usage_records(run,item,recorded_at,tokens,records,estimated_records) VALUES(1,1,?,?,1,0)`, base.UnixMilli(), int64(120)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO run_token_usage(run,token_count,token_records,token_estimated_records) VALUES(1,120,1,0)`); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestUsageAnalyticsEndpoint(t *testing.T) {
	a := usageApp(t)
	from := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	w := httptest.NewRecorder()
	a.usageAnalytics(w, httptest.NewRequest("GET", "/api/usage/analytics?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339)+"&timezone=Asia/Shanghai", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var report struct {
		Range struct {
			Interval string `json:"interval"`
			Timezone string `json:"timezone"`
		} `json:"range"`
		Totals struct {
			Tokens  int64 `json:"tokens"`
			Records int64 `json:"records"`
			Batches int64 `json:"batches"`
		} `json:"totals"`
		Batches struct {
			Items []struct {
				ID             int64 `json:"id"`
				LifetimeTokens int64 `json:"lifetime_tokens"`
			} `json:"items"`
		} `json:"batches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Totals.Tokens != 120 || report.Totals.Records != 1 || report.Totals.Batches != 1 {
		t.Fatal("totals", report.Totals)
	}
	if report.Range.Timezone != "Asia/Shanghai" || report.Range.Interval != "hour" {
		t.Fatal("range", report.Range)
	}
	if len(report.Batches.Items) != 1 || report.Batches.Items[0].ID != 1 || report.Batches.Items[0].LifetimeTokens != 120 {
		t.Fatal("batches", report.Batches.Items)
	}
}

func TestUsageAnalyticsRejectsInvalidParameters(t *testing.T) {
	a := usageApp(t)
	for _, query := range []string{
		"from=not-a-time",
		"to=not-a-time",
		"from=2026-09-24T00:00:00Z&to=2026-09-23T00:00:00Z",
		"timezone=Nowhere/Fake",
		"source=whenever",
		"sort=random",
		"profile=-1",
		"run=0x2",
		"page=0",
		"size=101",
	} {
		w := httptest.NewRecorder()
		a.usageAnalytics(w, httptest.NewRequest("GET", "/api/usage/analytics?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("%s: expected 400, got %d %s", query, w.Code, w.Body.String())
		}
	}
}
