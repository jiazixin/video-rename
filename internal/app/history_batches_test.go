package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"video-rename/internal/config"
)

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
			Model string `json:"model"`
			Title string `json:"model_config_title"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Model != "glm-4.6v" || rows[0].Title != "旅行模型配置" {
			t.Fatalf("%s model snapshot: %+v, %v", category, rows, err)
		}
	}
}
