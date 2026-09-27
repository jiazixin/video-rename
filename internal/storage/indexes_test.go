package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLargeLibraryQueriesSeekByEntry(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
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
	} {
		rows, err := s.Query("EXPLAIN QUERY PLAN "+tc.query, tc.args...)
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
