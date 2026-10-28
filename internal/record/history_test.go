package record

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"video-rename/internal/storage"
)

func TestHistoryModelSourceSeeksByEntry(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, category := range []string{"success", "failure"} {
		for _, source := range []string{"primary", "fallback", "unknown"} {
			t.Run(category+"/"+source, func(t *testing.T) {
				if err := db.View(t.Context(), func(tx *sql.Tx) error {
					query := "EXPLAIN QUERY PLAN SELECT COUNT(*) FROM events e" + historyAttemptJoin + " WHERE e.category=? AND e.action='automatic'"
					args := []any{category}
					if source == "unknown" {
						query += " AND a.item IS NULL"
					} else {
						query += " AND a.fallback=?"
						args = append(args, source == "fallback")
					}
					rows, err := tx.QueryContext(t.Context(), query, args...)
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
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestHistoryTokenTotalsSeekByItem(t *testing.T) {
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(t.Context(), func(tx *sql.Tx) error {
		for _, aggregate := range []string{"SUM(t.tokens)", "MAX(t.estimated_records)"} {
			rows, err := tx.QueryContext(t.Context(), "EXPLAIN QUERY PLAN SELECT (SELECT "+aggregate+historyTokenFrom+") FROM events e WHERE e.category='success' ORDER BY e.id DESC LIMIT 100")
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
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
