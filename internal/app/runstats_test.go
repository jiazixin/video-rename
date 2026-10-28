package app

import (
	"database/sql"
	"encoding/json/v2"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func runsAnalyticsApp(t *testing.T) *App {
	t.Helper()
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdown(t, a) })
	base := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	if _, err := storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,started,finished,discovered,done,skipped,failed,request_key,config_summary,model_config_id,model_config_title) VALUES(1,'manual','[]',1,'completed','2026-09-23T08:00:00.000Z','2026-09-23T08:00:00.000Z','2026-09-23T09:00:00.000Z',1,1,0,0,'k','{"model":"vision","provider":"custom","protocol":"openai"}',1,'默认')`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(500,1,'clip.mp4','file','i',1,0,'o')`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO run_items(id,run,entry,identity,size,mtime_ns,status,started_ms,finished_ms) VALUES(1,1,500,'i',1,0,'done',?,?)`, base.Add(time.Minute).UnixMilli(), base.Add(2*time.Minute).UnixMilli())
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRunsAnalyticsEndpoint(t *testing.T) {
	a := runsAnalyticsApp(t)
	from := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	w := httptest.NewRecorder()
	a.runsAnalytics(w, httptest.NewRequest("GET", "/api/runs/analytics?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339)+"&timezone=Asia/Shanghai", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var report struct {
		Range struct {
			Interval string `json:"interval"`
			Timezone string `json:"timezone"`
		} `json:"range"`
		Totals struct {
			Batches    int64 `json:"batches"`
			Completed  int64 `json:"completed"`
			Done       int64 `json:"done"`
			TimedItems int64 `json:"timed_items"`
			AvgItemMs  int64 `json:"avg_item_ms"`
		} `json:"totals"`
		Batches struct {
			Items []struct {
				ID         int64 `json:"id"`
				DurationMs int64 `json:"duration_ms"`
				AvgItemMs  int64 `json:"avg_item_ms"`
			} `json:"items"`
		} `json:"batches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Totals.Batches != 1 || report.Totals.Completed != 1 || report.Totals.Done != 1 {
		t.Fatal("totals", report.Totals)
	}
	if report.Range.Timezone != "Asia/Shanghai" || report.Range.Interval != "hour" {
		t.Fatal("range", report.Range)
	}
	if report.Totals.TimedItems != 1 || report.Totals.AvgItemMs != 60_000 {
		t.Fatal("item timing", report.Totals)
	}
	if len(report.Batches.Items) != 1 || report.Batches.Items[0].ID != 1 || report.Batches.Items[0].DurationMs != 3_600_000 || report.Batches.Items[0].AvgItemMs != 60_000 {
		t.Fatal("batch rows", report.Batches.Items)
	}
}

func TestRunsAnalyticsRejectsInvalidParameters(t *testing.T) {
	a := runsAnalyticsApp(t)
	for _, query := range []string{
		"from=not-a-time",
		"to=not-a-time",
		"from=2026-09-24T00:00:00Z&to=2026-09-23T00:00:00Z",
		"timezone=Nowhere/Fake",
		"source=whenever",
		"sort=random",
		"profile=-1",
		"offset=-1",
		"limit=101",
	} {
		w := httptest.NewRecorder()
		a.runsAnalytics(w, httptest.NewRequest("GET", "/api/runs/analytics?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("%s: expected 400, got %d %s", query, w.Code, w.Body.String())
		}
	}
}

// The literal /api/runs/analytics route must win over the /api/runs/{id}
// wildcard instead of failing to parse "analytics" as a batch id.
func TestRunsAnalyticsRoutePrecedence(t *testing.T) {
	a := runsAnalyticsApp(t)
	handler := a.Handler()
	login := httptest.NewRecorder()
	loginRequest := jsonRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password-123"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(login, loginRequest)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body.String())
	}
	cookie := login.Header().Get("Set-Cookie")
	if cookie == "" {
		t.Fatal("missing session cookie")
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/runs/analytics", nil)
	request.Header.Set("Cookie", strings.Split(cookie, ";")[0])
	handler.ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatalf("analytics route should reach its handler: %d %s", w.Code, w.Body.String())
	}
	var report struct {
		Totals struct {
			Batches int64 `json:"batches"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil || report.Totals.Batches != 1 {
		t.Fatal("mux response", err, w.Body.String())
	}
}
