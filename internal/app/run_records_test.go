package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"testing"

	"video-rename/internal/config"
)

func TestRunRecordViews(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	for id := range 2 {
		if _, err := a.DB.Exec(`INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(?,'manual','[]',0,'running','now',?)`, id+1, fmt.Sprintf("records-%d", id)); err != nil {
			t.Fatal(err)
		}
	}
	for id := 1; id <= 334; id++ {
		run, status, finished, started := 1, "done", id*100, 0
		if id%3 == 0 {
			status = "failed"
		}
		switch id {
		case 1:
			finished = 50000
		case 3:
			finished = 55000
		case 331:
			status, finished = "queued", 0
		case 332:
			status, finished, started = "running", 0, 60000
		case 333, 334:
			run, status, finished = 2, "failed", 999999
		}
		if _, err := a.DB.Exec(`INSERT INTO run_items(id,run,entry,identity,size,mtime,status,original_path,started_ms,finished_ms) VALUES(?,?,1,?,0,'now',?,?,?,?)`, id, run, fmt.Sprint(id), status, fmt.Sprintf("clip-%d.mp4", id), started, finished); err != nil {
			t.Fatal(err)
		}
	}
	type record struct {
		ID        int64  `json:"id"`
		Status    string `json:"status"`
		UpdatedMS int64  `json:"updated_ms"`
	}
	type recordsPage struct {
		Items []record `json:"items"`
		Total int      `json:"total"`
		Page  int      `json:"page"`
	}
	read := func(query string) recordsPage {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/runs/1/items?"+query, nil)
		r.SetPathValue("id", "1")
		a.items(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
		var page recordsPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	logs := read("view=logs&page=1")
	if logs.Total != 331 || len(logs.Items) != 100 || logs.Items[0].ID != 332 || logs.Items[1].ID != 3 || logs.Items[2].ID != 1 || logs.Items[0].UpdatedMS != 60000 {
		t.Fatalf("latest batch records: %+v", logs)
	}
	for _, item := range logs.Items {
		if item.Status == "queued" || item.ID > 332 {
			t.Fatalf("unprocessed or other batch item: %+v", item)
		}
	}
	failures := read("view=failures&page=1")
	if failures.Total != 110 || len(failures.Items) != 100 || failures.Items[0].ID != 3 || failures.Items[1].ID != 330 {
		t.Fatalf("whole batch failures: %+v", failures)
	}
	for _, item := range failures.Items {
		if item.Status != "failed" {
			t.Fatalf("non-failure: %+v", item)
		}
	}
	last := read("view=failures&page=9223372036854775807")
	if last.Page != 2 || len(last.Items) != 10 || last.Total != 110 {
		t.Fatalf("last failure page: %+v", last)
	}
	files := read("page=1")
	if files.Total != 332 || files.Items[0].ID != 1 {
		t.Fatalf("file list changed: %+v", files)
	}
	if _, err := a.DB.Exec(`UPDATE run_items SET status='failed',finished_ms=70000 WHERE id=331`); err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"logs", "failures"} {
		page := read("view=" + view + "&page=1")
		if page.Items[0].ID != 331 {
			t.Fatalf("new result missing from %s: %+v", view, page)
		}
	}
	if _, err := a.DB.Exec(`DELETE FROM run_items WHERE run=1`); err != nil {
		t.Fatal(err)
	}
	empty := read("view=failures&page=2")
	if empty.Total != 0 || empty.Page != 1 || len(empty.Items) != 0 {
		t.Fatalf("empty view: %+v", empty)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/runs/1/items?view=invalid&page=1", nil)
	r.SetPathValue("id", "1")
	a.items(w, r)
	if w.Code != 400 {
		t.Fatalf("invalid view accepted: %d", w.Code)
	}
}
