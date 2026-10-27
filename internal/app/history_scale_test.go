//go:build scale

package app

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"video-rename/internal/storage"
)

const historyScaleRecords = 11000

type historyScaleRecord struct {
	ID             int64  `json:"id"`
	Source         string `json:"model_source"`
	Model          string `json:"model"`
	Title          string `json:"model_config_title"`
	Tokens         int64  `json:"tokens"`
	TokenEstimated bool   `json:"token_estimated"`
}

type historyScalePage struct {
	Items []historyScaleRecord `json:"items"`
	Total int                  `json:"total"`
	Page  int                  `json:"page"`
	Next  int64                `json:"next"`
}

// historyScaleFixture models one completed batch without touching media or
// starting workers. The fallback successes are distributed throughout the batch.
func historyScaleFixture(tb testing.TB) (*App, []historyScaleRecord) {
	tb.Helper()
	db, err := storage.Open(tb.Context(), filepath.Join(tb.TempDir(), "history.sqlite"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := db.Close(); err != nil {
			tb.Error(err)
		}
	})
	tx, err := db.BeginTx(tb.Context(), nil)
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(tb.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,request_key,model_config_title,config_summary)
		VALUES(1,'manual','[]',0,'completed','2026-10-04T00:00:00Z','history-scale','primary configuration','{"model":"primary-model"}')`); err != nil {
		tb.Fatal(err)
	}
	prepare := func(query string) *sql.Stmt {
		tb.Helper()
		stmt, err := tx.PrepareContext(tb.Context(), query)
		if err != nil {
			tb.Fatal(err)
		}
		return stmt
	}
	entry := prepare(`INSERT INTO entries(id,name,kind,identity,size,mtime,observed)
		VALUES(?,?,'file',?,0,'2026-10-04T00:00:00Z','2026-10-04T00:00:00Z')`)
	defer entry.Close()
	item := prepare(`INSERT INTO run_items(id,run,entry,identity,size,mtime,status,original_path)
		VALUES(?,1,?,?,0,'2026-10-04T00:00:00Z','success',?)`)
	defer item.Close()
	attempt := prepare(`INSERT INTO model_attempts(item,attempt,model_config_id,model_config_title,model,fallback,started,duration_ms,status,message)
		VALUES(?,?,?,?,?,?,'2026-10-04T00:00:00Z',10,?,'')`)
	defer attempt.Close()
	usage := prepare(`INSERT INTO token_usage_records(run,item,attempt,model_config_id,model_config_title,recorded_at,tokens,records,estimated_records)
		VALUES(1,?,?,?,?,1791072000000,?,1,?)`)
	defer usage.Close()
	event := prepare(`INSERT INTO events(id,category,action,entry,run,old_path,new_path,created)
		VALUES(?,'success','automatic',?,1,?,?,'2026-10-04T00:00:00Z')`)
	defer event.Close()
	exec := func(stmt *sql.Stmt, args ...any) {
		tb.Helper()
		if _, err := stmt.ExecContext(tb.Context(), args...); err != nil {
			tb.Fatal(err)
		}
	}
	expected := make([]historyScaleRecord, historyScaleRecords)
	for i := range historyScaleRecords {
		id := int64(i + 1)
		name, identity := fmt.Sprintf("clip-%05d.mp4", id), fmt.Sprintf("file-%d", id)
		fallback := (i+1)*600/historyScaleRecords != i*600/historyScaleRecords
		estimated := i%7 == 0
		primaryTokens := int64(100 + i%23)
		exec(entry, id, name, identity)
		exec(item, id, id, identity, name)
		status := "success"
		if fallback {
			status = "failed"
		}
		exec(attempt, id, 1, 1, "primary configuration", "primary-model", false, status)
		exec(usage, id, 1, 1, "primary configuration", primaryTokens, estimated)
		row := historyScaleRecord{ID: id, Source: "primary", Model: "primary-model", Title: "primary configuration", Tokens: primaryTokens, TokenEstimated: estimated}
		if fallback {
			fallbackTokens := int64(200 + i%17)
			exec(attempt, id, 2, 2, "fallback configuration", "fallback-model", true, "success")
			exec(usage, id, 2, 2, "fallback configuration", fallbackTokens, false)
			row.Source, row.Model, row.Title = "fallback", "fallback-model", "fallback configuration"
			row.Tokens += fallbackTokens
		}
		exec(event, id, id, name, "renamed-"+name)
		expected[historyScaleRecords-1-i] = row
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return &App{DB: db}, expected
}

func historyScaleURL(source string, page int) string {
	return fmt.Sprintf("/api/logs?category=success&model_source=%s&limit=100&page=%d", source, page)
}

func TestScaleHistoryModelSource(t *testing.T) {
	a, expected := historyScaleFixture(t)
	for _, tc := range []struct {
		name, source string
		total        int
	}{
		{"fallback", "fallback", 600},
		{"primary", "primary", 10400},
		{"unknown", "unknown", 0},
		{"all", "", historyScaleRecords},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var matches []historyScaleRecord
			for _, row := range expected {
				if tc.source == "" || row.Source == tc.source {
					matches = append(matches, row)
				}
			}
			if len(matches) != tc.total {
				t.Fatalf("fixture count=%d, want %d", len(matches), tc.total)
			}
			last := max(1, (tc.total+99)/100)
			for _, requested := range []int{1, last + 1} {
				url := historyScaleURL(tc.source, requested)
				w := httptest.NewRecorder()
				start := time.Now()
				a.logs(w, httptest.NewRequest(http.MethodGet, url, nil))
				elapsed := time.Since(start)
				if w.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", url, w.Code, w.Body.String())
				}
				var page historyScalePage
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				wantPage := min(requested, last)
				startIndex := (wantPage - 1) * 100
				want := matches[startIndex:min(startIndex+100, len(matches))]
				if page.Total != tc.total || page.Page != wantPage || len(page.Items) != len(want) {
					t.Fatalf("total=%d page=%d items=%d, want total=%d page=%d items=%d", page.Total, page.Page, len(page.Items), tc.total, wantPage, len(want))
				}
				for i, row := range page.Items {
					if row != want[i] {
						t.Fatalf("item %d: got %+v, want %+v", i, row, want[i])
					}
					if i > 0 && page.Items[i-1].ID <= row.ID {
						t.Fatal("history is not in descending ID order")
					}
				}
				wantNext := int64(0)
				if wantPage < last {
					wantNext = want[len(want)-1].ID
				}
				if page.Next != wantNext {
					t.Fatalf("next=%d, want %d", page.Next, wantNext)
				}
				t.Logf("%d records, %d pages, page %d: %s", page.Total, last, page.Page, elapsed)
			}
		})
	}
}

func BenchmarkHistoryModelSource(b *testing.B) {
	a, _ := historyScaleFixture(b)
	for _, tc := range []struct {
		name, source string
		page         int
	}{
		{"fallback_first", "fallback", 1},
		{"fallback_last", "fallback", 6},
		{"primary_first", "primary", 1},
		{"unknown", "unknown", 1},
		{"all_first", "", 1},
	} {
		b.Run(tc.name, func(b *testing.B) {
			request := httptest.NewRequest(http.MethodGet, historyScaleURL(tc.source, tc.page), nil)
			b.ReportAllocs()
			for b.Loop() {
				w := httptest.NewRecorder()
				a.logs(w, request)
				if w.Code != http.StatusOK {
					b.Fatalf("%d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
