package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"video-rename/internal/config"
)

func TestExtractionHistoryGroupsFiltersPaginationAndClear(t *testing.T) {
	a, err := New(&config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, status := range []string{"success", "failure", "running"} {
		for i := range 53 {
			if _, err = a.DB.Exec(`INSERT INTO extraction_records(run,path,source,status,started,finished,duration_ms,processing_log) VALUES(7,?,'manual',?,'2026-09-26T01:00:00Z','2026-09-26T01:00:02Z',2000,'decoder detail')`, fmt.Sprintf("clip%d.mp4", i), status); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = a.DB.Exec(`INSERT INTO extraction_records(path,source,status,started) VALUES('unbatched.mp4','automatic','success','2026-09-25T01:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	type group struct {
		Items []extractionRecord `json:"items"`
		Total int                `json:"total"`
		Next  int64              `json:"next"`
	}
	var result struct {
		Success group `json:"success"`
		Failure group `json:"failure"`
		Running int   `json:"running"`
	}
	get := func(query string) {
		t.Helper()
		w := httptest.NewRecorder()
		a.extractions(w, httptest.NewRequest("GET", "/api/extractions?"+query, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(w.Body.String(), "decoder detail") {
			t.Fatal("list eagerly returned diagnostics")
		}
	}
	get("run=7&q=clip&limit=50&from=2026-09-26T00:00:00Z&to=2026-09-26T23:59:59Z")
	if result.Success.Total != 53 || result.Failure.Total != 53 || len(result.Success.Items) != 50 || len(result.Failure.Items) != 50 || result.Running != 53 {
		t.Fatal(result)
	}
	get(fmt.Sprintf("run=7&limit=50&success_before=%d", result.Success.Next))
	if len(result.Success.Items) != 3 || result.Success.Next != 0 || len(result.Failure.Items) != 50 {
		t.Fatal("pagination not independent", result)
	}
	get("q=unbatched")
	if len(result.Success.Items) != 1 || result.Success.Items[0].Run != 0 || result.Failure.Total != 0 {
		t.Fatal(result)
	}
	get("from=2026-09-27T00:00:00Z")
	if result.Success.Total != 0 || result.Failure.Total != 0 {
		t.Fatal(result)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/extractions/54", nil)
	r.SetPathValue("id", "54")
	a.extractionDetail(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "decoder detail") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/extractions", "/api/extractions/54"} {
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatal("unprotected extraction history", path, w.Code)
		}
	}
	for _, query := range []string{"run=-1", "run=bad", "from=invalid"} {
		w = httptest.NewRecorder()
		a.extractions(w, httptest.NewRequest("GET", "/api/extractions?"+query, nil))
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	w = httptest.NewRecorder()
	a.clearExtractions(w, httptest.NewRequest("POST", "/api/extractions/clear", strings.NewReader(`{"all":true}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	get("")
	if result.Success.Total != 0 || result.Failure.Total != 0 || result.Running != 53 {
		t.Fatal("clear deleted active records", result)
	}
}
