package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func TestHistoryModelSourceSeeksByEntry(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, category := range []string{"success", "failure"} {
		for _, source := range []string{"primary", "fallback", "unknown"} {
			t.Run(category+"/"+source, func(t *testing.T) {
				query := "EXPLAIN QUERY PLAN SELECT COUNT(*) FROM events e" + historyAttemptJoin + " WHERE e.category=? AND e.action='automatic'"
				args := []any{category}
				if source == "unknown" {
					query += " AND a.item IS NULL"
				} else {
					query += " AND a.fallback=?"
					args = append(args, source == "fallback")
				}
				rows, err := db.Read().QueryContext(t.Context(), query, args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var plan strings.Builder
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					plan.WriteString(detail + "\n")
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(plan.String(), "item_entry (entry=?)") {
					t.Fatalf("model source lookup must seek by entry rather than scan the batch:\n%s", plan.String())
				}
			})
		}
	}
}

func TestHistoryTokenTotalsSeekByItem(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, aggregate := range []string{"SUM(t.tokens)", "MAX(t.estimated_records)"} {
		rows, err := db.Read().QueryContext(t.Context(), "EXPLAIN QUERY PLAN SELECT (SELECT "+aggregate+historyTokenFrom+") FROM events e WHERE e.category='success' ORDER BY e.id DESC LIMIT 100")
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), "item_entry (entry=?)") || !strings.Contains(plan.String(), "sqlite_autoindex_token_usage_records_1 (item=?)") {
			t.Fatalf("token totals must seek by entry and item rather than scan the batch:\n%s", plan.String())
		}
	}
}

func TestHistoryBatchFiltering(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, category := range []string{"success", "failure"} {
		for range 105 {
			if _, err := a.DB.Exec("INSERT INTO events(category,action,run,old_path,message,created) VALUES(?,'automatic',7,'clip.mp4','test','2026-09-21')", category); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.DB.Exec("INSERT INTO events(category,action,run,old_path,message,created) VALUES(?,'automatic',8,'other.mp4','test','2026-09-21'),(?,'manual',0,'manual.mp4','test','2026-09-21')", category, category); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?category="+category+"&run=7&q=clip&action=automatic&from=2026-09-20&to=2026-09-22", nil))
		var rows []struct {
			ID  int64 `json:"id"`
			Run int64 `json:"run"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 100 {
			t.Fatalf("%s: got %d records", category, len(rows))
		}
		for _, limit := range []int{50, 100, 200} {
			w := httptest.NewRecorder()
			a.logs(w, httptest.NewRequest("GET", fmt.Sprintf("/api/logs?category=%s&run=7&limit=%d&paged=1", category, limit), nil))
			var page struct {
				Items []map[string]any `json:"items"`
				Total int              `json:"total"`
				Next  int64            `json:"next"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Total != 105 || len(page.Items) != min(limit, 105) || (page.Next > 0) != (limit < 105) {
				t.Fatalf("bad page: %+v", page)
			}
			if page.Next > 0 {
				w = httptest.NewRecorder()
				a.logs(w, httptest.NewRequest("GET", fmt.Sprintf("/api/logs?category=%s&run=7&limit=%d&paged=1&before=%d", category, limit, page.Next), nil))
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.Total != 105 || len(page.Items) != min(limit, 105-limit) {
					t.Fatalf("bad next page: %+v", page)
				}
			}
		}
		for _, row := range rows {
			if row.Run != 7 {
				t.Fatalf("wrong batch: %d", row.Run)
			}
		}
		w = httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?category="+category+"&run=7&before="+strconv.FormatInt(rows[len(rows)-1].ID, 10), nil))
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 5 {
			t.Fatalf("next page: got %d records", len(rows))
		}
		w = httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?category="+category+"&run=8&q=clip", nil))
		if w.Body.String() != "[]" {
			t.Fatalf("combined filter: %s", w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	a.logBatches(w, httptest.NewRequest("GET", "/api/logs/batches", nil))
	if w.Body.String() != "[8,7]" {
		t.Fatalf("batch options: %s", w.Body.String())
	}
	for _, run := range []string{"0", "-1", "bad"} {
		w = httptest.NewRecorder()
		a.logs(w, httptest.NewRequest("GET", "/api/logs?category=success&run="+run, nil))
		if w.Code != 400 {
			t.Fatalf("invalid batch %s accepted", run)
		}
	}
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/logs/batches", nil))
	if w.Code != 401 {
		t.Fatal("batch list requires authentication")
	}
}

func TestHistoryShowsBatchModelSnapshot(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Workers: 1, Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	result, err := a.DB.Exec(`INSERT INTO runs(source,targets,recursive,status,created,request_key,model_config_title,config_summary)
VALUES('manual','[]',1,'completed','now','history-model-run','旅行模型配置','{"model":"glm-4.6v"}')`)
	if err != nil {
		t.Fatal(err)
	}
	run, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range []string{"success", "failure"} {
		if _, err = a.DB.Exec("INSERT INTO events(category,action,run,created) VALUES(?,'automatic',?,'now')", category, run); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		a.logs(response, httptest.NewRequest("GET", "/api/logs?category="+category, nil))
		var rows []struct {
			Model  string `json:"model"`
			Title  string `json:"model_config_title"`
			Source string `json:"model_source"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Model != "glm-4.6v" || rows[0].Title != "旅行模型配置" || rows[0].Source != "unknown" {
			t.Fatalf("%s model snapshot: %+v, %v", category, rows, err)
		}
	}
}

func TestHistoryUsesActualModelAttempt(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, query := range []string{
		`INSERT INTO entries(id,name,kind,identity,size,mtime,observed) VALUES
		(2,'fallback.mp4','file','file-2',0,'now','now'),
		(3,'primary.mp4','file','file-3',0,'now','now'),
		(4,'failed.mp4','file','file-4',0,'now','now'),
		(5,'unknown.mp4','file','file-5',0,'now','now'),
		(6,'no-success.mp4','file','file-6',0,'now','now'),
		(7,'multiple-items.mp4','file','file-7',0,'now','now')`,
		`INSERT INTO runs(id,source,targets,recursive,status,created,request_key,model_config_title,config_summary) VALUES
		(101,'manual','[]',0,'completed','now','actual-model-1','批次主配置','{"model":"batch-primary"}'),
		(102,'manual','[]',0,'completed','now','actual-model-2','另一个批次','{"model":"other-batch"}')`,
		`INSERT INTO run_items(id,run,entry,identity,size,mtime) VALUES
		(201,101,2,'file-2',0,'now'),(202,101,3,'file-3',0,'now'),
		(203,101,4,'file-4',0,'now'),(204,101,5,'file-5',0,'now'),
		(205,101,6,'file-6',0,'now'),(206,102,2,'file-2',0,'now'),
		(207,101,7,'file-7',0,'now'),(208,101,7,'file-7-new',0,'now')`,
		`INSERT INTO model_attempts(item,attempt,model_config_id,model_config_title,model,fallback,started,duration_ms,status,message) VALUES
		(201,1,1,'主配置快照','primary-model',0,'now',1,'failed',''),
		(201,2,2,'兜底配置快照','fallback-model',1,'now',1,'success',''),
		(202,1,1,'主配置快照','primary-model',0,'now',1,'failed',''),
		(202,2,1,'主配置快照','primary-model',0,'now',1,'success',''),
		(203,1,1,'主配置快照','primary-model',0,'now',1,'failed',''),
		(203,2,2,'兜底配置快照','fallback-model',1,'now',1,'failed',''),
		(205,1,1,'主配置快照','primary-model',0,'now',1,'failed',''),
		(206,9,3,'其他批次配置','other-model',0,'now',1,'success',''),
		(207,1,2,'兜底配置快照','fallback-model',1,'now',1,'success',''),
		(207,2,1,'主配置快照','primary-model',0,'now',1,'success',''),
		(207,3,2,'兜底配置快照','fallback-model',1,'now',1,'failed',''),
		(208,1,2,'兜底配置快照','fallback-model',1,'now',1,'failed','')`,
		`INSERT INTO token_usage_records(run,item,attempt,recorded_at,tokens,estimated_records) VALUES
		(101,201,1,1,11,0),(101,201,2,2,17,1),(102,206,1,3,99,0),
		(101,207,1,4,13,0),(101,207,2,5,19,1),(101,207,3,6,5,0),(101,208,1,7,7,0)`,
		`INSERT INTO events(category,action,entry,run,old_path,created) VALUES
		('success','automatic',2,101,'fallback.mp4','now'),
		('success','automatic',3,101,'primary.mp4','now'),
		('success','automatic',5,101,'unknown.mp4','now'),
		('success','automatic',6,101,'no-success.mp4','now'),
		('success','manual',2,101,'manual.mp4','now'),
		('success','restore',2,101,'restore.mp4','now'),
		('success','automatic',2,102,'other-run.mp4','now'),
		('failure','automatic',4,101,'failed.mp4','now'),
		('failure','automatic',3,101,'rename-failed.mp4','now'),
		('success','automatic',7,101,'multiple-items.mp4','now'),
		('failure','automatic',7,101,'retried-failed.mp4','now')`,
	} {
		if _, err := a.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	type record struct {
		Path           string `json:"old_path"`
		Model          string `json:"model"`
		Title          string `json:"model_config_title"`
		Source         string `json:"model_source"`
		Tokens         int64  `json:"tokens"`
		TokenEstimated bool   `json:"token_estimated"`
	}
	expected := map[string]map[string]record{
		"success": {
			"fallback.mp4":       {"fallback.mp4", "fallback-model", "兜底配置快照", "fallback", 28, true},
			"primary.mp4":        {"primary.mp4", "primary-model", "主配置快照", "primary", 0, false},
			"unknown.mp4":        {"unknown.mp4", "batch-primary", "批次主配置", "unknown", 0, false},
			"no-success.mp4":     {"no-success.mp4", "batch-primary", "批次主配置", "unknown", 0, false},
			"manual.mp4":         {"manual.mp4", "batch-primary", "批次主配置", "", 28, true},
			"restore.mp4":        {"restore.mp4", "batch-primary", "批次主配置", "", 28, true},
			"other-run.mp4":      {"other-run.mp4", "other-model", "其他批次配置", "primary", 99, false},
			"multiple-items.mp4": {"multiple-items.mp4", "primary-model", "主配置快照", "primary", 44, true},
		},
		"failure": {
			"failed.mp4":         {"failed.mp4", "fallback-model", "兜底配置快照", "fallback", 0, false},
			"rename-failed.mp4":  {"rename-failed.mp4", "primary-model", "主配置快照", "primary", 0, false},
			"retried-failed.mp4": {"retried-failed.mp4", "fallback-model", "兜底配置快照", "fallback", 44, true},
		},
	}
	for category, all := range expected {
		for _, source := range []string{"", "primary", "fallback", "unknown"} {
			want := map[string]record{}
			for path, row := range all {
				if source == "" || row.Source == source {
					want[path] = row
				}
			}
			for _, suffix := range []string{"", "&paged=1", "&page=1"} {
				t.Run(category+source+suffix, func(t *testing.T) {
					w := httptest.NewRecorder()
					a.logs(w, httptest.NewRequest("GET", "/api/logs?category="+category+"&model_source="+source+suffix, nil))
					if w.Code != 200 {
						t.Fatal(w.Code, w.Body.String())
					}
					var rows []record
					if suffix == "" {
						err = json.Unmarshal(w.Body.Bytes(), &rows)
					} else {
						var page struct {
							Items []record `json:"items"`
							Total int      `json:"total"`
						}
						err = json.Unmarshal(w.Body.Bytes(), &page)
						rows = page.Items
						if page.Total != len(want) {
							t.Fatalf("total=%d, want %d", page.Total, len(want))
						}
					}
					if err != nil || len(rows) != len(want) {
						t.Fatal(rows, err)
					}
					for _, row := range rows {
						if row != want[row.Path] {
							t.Fatalf("got %+v, want %+v", row, want[row.Path])
						}
					}
				})
			}
		}
	}
	w := httptest.NewRecorder()
	a.logs(w, httptest.NewRequest("GET", "/api/logs?category=success&model_source=invalid", nil))
	if w.Code != 400 {
		t.Fatal("invalid model source accepted", w.Code, w.Body.String())
	}
}
