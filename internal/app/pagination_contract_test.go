package app

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"testing"
)

func TestListDefaultsUseOffsetPagination(t *testing.T) {
	c := newContractClient(t)
	for _, path := range []string{"/api/directories/1/entries", "/api/runs", "/api/logs?category=success", "/api/notifications/history"} {
		t.Run(path, func(t *testing.T) {
			response := c.call(contractRequest{method: http.MethodGet, path: path})
			requireContractStatus(t, response, http.StatusOK)
			var page struct {
				Items  []any `json:"items"`
				Offset *int  `json:"offset"`
				Limit  int   `json:"limit"`
				Total  *int  `json:"total"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Offset == nil || *page.Offset != 0 || page.Limit <= 0 || page.Total == nil || page.Items == nil {
				t.Fatalf("got %s, want offset page with items and total", response.Body.String())
			}
			var fields map[string]jsontext.Value
			if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["page"]; exists {
				t.Fatal("got retired page field, want only offset pagination")
			}
		})
	}
}

func TestListQueriesRejectRetiredPagination(t *testing.T) {
	c := newContractClient(t)
	for _, path := range []string{
		"/api/directories/1/entries?page=1", "/api/directories/1/entries?generation=1",
		"/api/runs?before=1", "/api/logs?category=success&before=1", "/api/logs?category=success&paged=1",
		"/api/extractions?success_before=1", "/api/extractions?failure_before=1",
		"/api/notifications/history?before=1",
		"/api/notifications/history?offset=-1", "/api/notifications/history?limit=0", "/api/notifications/history?limit=12", "/api/logs?category=success&limit=201",
		"/api/directories/1/entries?page=1&page=2", "/api/directories/1/entries?kind=unsupported",
		"/api/directories/1/entries?sort=unsupported", "/api/logs?category=success&from=2026-10-28",
	} {
		t.Run(path, func(t *testing.T) {
			response := c.call(contractRequest{method: http.MethodGet, path: path})
			requireContractStatus(t, response, http.StatusBadRequest)
		})
	}
}

func TestEntryHistoryCursorHasNoGapsAndRejectsOldQuery(t *testing.T) {
	c := newContractClient(t)
	if err := c.app.db.Write(t.Context(), func(tx *sql.Tx) error {
		for i := range 101 {
			if _, err := tx.ExecContext(t.Context(), "INSERT INTO operations(entry,parent,old_name,new_name,identity,size,mtime_ns,kind,source,status,request_key,created) VALUES(1,1,'old','new','fixture',0,0,'directory','manual','committed',?,'2026-10-28T00:00:00.000Z')", fmt.Sprint(i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var first struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	r := c.call(contractRequest{method: "GET", path: "/api/entries/1/history"})
	requireContractStatus(t, r, 200)
	if err := json.Unmarshal(r.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || first.Items[99].ID != 2 || first.NextCursor == "" {
		t.Fatalf("incomplete first page: %s", r.Body.String())
	}
	r = c.call(contractRequest{method: "GET", path: "/api/entries/1/history?cursor=" + first.NextCursor})
	requireContractStatus(t, r, 200)
	first.NextCursor = ""
	if err := json.Unmarshal(r.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != 1 || first.NextCursor != "" {
		t.Fatalf("incomplete last page: %s", r.Body.String())
	}
	for _, query := range []string{"before=2", "cursor=bad", "cursor=-1", "cursor=0", "cursor=1&cursor=2"} {
		requireContractStatus(t, c.call(contractRequest{method: "GET", path: "/api/entries/1/history?" + query}), 400)
	}
}
