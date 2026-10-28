package app

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"video-rename/internal/config"
	"video-rename/internal/storage"
)

func TestNumberedHistoryExtractionAndItemPages(t *testing.T) {
	a, err := New(t.Context(), &config.Config{Path: t.TempDir(), Storage: t.TempDir(), Auth: config.AuthConfig{Username: "admin", Password: "test-password-123"}}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, a)
	if _, err = storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[]',0,'completed','2026-10-01T00:00:00.000Z','pages')`)
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 205 {
		name := fmt.Sprintf("clip%03d.mp4", i)
		for _, query := range []string{
			`INSERT INTO events(category,action,old_path,created) VALUES('success','manual',?,'2026-10-01T00:00:00.000Z')`,
			`INSERT INTO extraction_records(path,source,status,started) VALUES(?,'manual','success','2026-10-01T00:00:00.000Z')`,
			`INSERT INTO run_items(run,entry,identity,size,mtime_ns,original_path) VALUES(1,1,?,0,0,'original')`,
		} {
			if _, err = storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), query, name) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	type result struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
		Page  int `json:"page"`
	}
	read := func(handler http.HandlerFunc, url string, group string) result {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", url, nil)
		r.SetPathValue("id", "1")
		handler(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", url, w.Code, w.Body.String())
		}
		var page result
		if group != "" {
			var envelope struct {
				Success result `json:"success"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			page = envelope.Success
		} else if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}

		return page
	}
	for _, tc := range []struct {
		name          string
		handler       http.HandlerFunc
		prefix, group string
		lastFirst     int64
	}{
		{"logs", a.logs, "/logs?category=success&limit=100&page=", "", 5},
		{"extractions", a.extractions, "/extractions?limit=100&success_page=", "success", 5},
		{"items", a.items, "/runs/1/items?page=", "", 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, number := range []string{"3", "9223372036854775807"} {
				p := read(tc.handler, tc.prefix+number, tc.group)
				if p.Page != 3 || p.Total != 205 || len(p.Items) != 5 || p.Items[0].ID != tc.lastFirst {
					t.Fatalf("last page: %+v", p)
				}
			}
			p := read(tc.handler, tc.prefix+"-5", tc.group)
			if p.Page != 1 || len(p.Items) != 100 {
				t.Fatalf("first page: %+v", p)
			}
		})
	}
	for _, table := range []string{"events", "extraction_records", "run_items"} {
		if _, err = storage.WriteValue(t.Context(), a.db, func(tx *sql.Tx) (sql.Result, error) { return tx.ExecContext(t.Context(), "DELETE FROM "+table) }); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []result{read(a.logs, "/logs?category=success&page=99", ""), read(a.extractions, "/extractions?success_page=99", "success"), read(a.items, "/runs/1/items?page=99", "")} {
		if p.Page != 1 || p.Total != 0 || len(p.Items) != 0 {
			t.Fatalf("empty page: %+v", p)
		}
	}
}
