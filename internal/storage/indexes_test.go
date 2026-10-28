package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestLargeLibraryQueriesSeekByEntry(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		query, seek string
		args        []any
	}{
		{"SELECT id FROM entries WHERE parent=? AND present=1 AND id>? ORDER BY id LIMIT 128", "id>?", []any{1, 100}},
		{"UPDATE directory_items SET name=? WHERE dir=? AND entry=?", "entry=?", []any{"new.mp4", 1, 100}},
		{"DELETE FROM events WHERE category='failure' AND action='automatic' AND entry=?", "entry=?", []any{100}},
		{"UPDATE entries SET present=0 WHERE parent=? AND present=1 AND id NOT IN (SELECT entry FROM directory_items WHERE dir=? AND generation=?)", "entry_parent_id", []any{1, 1, 1}},
		{"SELECT id FROM runs WHERE status='queued' ORDER BY id LIMIT 20", "run_status", nil},
		{"SELECT id FROM runs WHERE analysis_start_ms>=? AND analysis_start_ms<?", "run_analysis_start", []any{1000, 2000}},
		{"SELECT id FROM entries WHERE parent=? AND present=1 AND kind='directory'", "entry_directories", []any{1}},
		{"SELECT id FROM entries WHERE present=0 AND observed GLOB 'directory-scan:*'", "entry_scan_orphan", nil},
		{"SELECT entry FROM operations WHERE status IN ('prepared','review')", "operation_pending", nil},
		{"SELECT id FROM runs INDEXED BY run_active WHERE status IN ('queued','scanning','running','paused','cancelling') ORDER BY id", "run_active", nil},
		{"SELECT id FROM run_items WHERE run=? AND id>? ORDER BY id LIMIT 100", "item_page", []any{1, 0}},
		{"SELECT id FROM run_items WHERE run=? AND status<>'queued' ORDER BY MAX(started_ms,finished_ms) DESC,id DESC LIMIT 100", "item_logs", []any{1}},
		{"SELECT id FROM run_items WHERE run=? AND status='failed' ORDER BY MAX(started_ms,finished_ms) DESC,id DESC LIMIT 100", "item_failures", []any{1}},
		{"SELECT count(*) FROM events WHERE category=? AND run=?", "category=? AND run=?", []any{"success", 1}},
	} {
		rows, err := s.write.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		t.Logf("%s: %s", tc.query, plan)
		if !strings.Contains(plan, tc.seek) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("query scans/sorts siblings or history instead of seeking: %s", plan)
		}
	}
}

func TestCandidateDeletionDoesNotScanForeignKeyTables(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "foreign-keys.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.write.QueryContext(t.Context(), "EXPLAIN QUERY PLAN DELETE FROM entries WHERE id=? AND present=0", 123)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SCAN ") {
			t.Errorf("candidate deletion scans a foreign-key table: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisTimestampPreservesMillisecondBoundaries(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "time.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		started, created string
		want             int64
		wantError        bool
	}{
		{"2026-10-05T00:00:00.123Z", "", 1791158400123, false},
		{"", "1970-01-01T00:00:00.001Z", 1, false},
		{"1970-01-01T00:00:00.000Z", "1970-01-01T00:00:00.003Z", 0, false},
		{"", "", 0, false},
		{"2026-10-05T00:00:00.123999999Z", "", 0, true},
		{"", "1970-01-01T00:00:00.001999999Z", 0, true},
		{"invalid", "1970-01-01T00:00:00.002Z", 0, true},
		{"1970-01-01T00:00:00Z", "1970-01-01T00:00:00.003Z", 0, true},
		{"1970-01-01T01:00:00.000+01:00", "", 0, true},
	} {
		var got int64
		err := s.View(t.Context(), func(tx *sql.Tx) error {
			return tx.QueryRowContext(t.Context(), "SELECT video_rename_start_ms(?,?)", tc.started, tc.created).Scan(&got)
		})
		if (err != nil) != tc.wantError || !tc.wantError && got != tc.want {
			t.Fatalf("analysis timestamp: got %d error=%v, want %d error=%v (%+v)", got, err, tc.want, tc.wantError, tc)
		}
	}
}
