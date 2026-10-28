package job

import (
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"video-rename/internal/storage"
)

func TestRunPagesFilterBeforePagination(t *testing.T) {
	m, _ := setup(t)
	empty, err := m.ListPage(t.Context(), ListPageOptions{Category: "manual", Task: "", Offset: 4900, Limit: 50})
	if err != nil || empty.Total != 0 || empty.Offset != 0 || empty.Items == nil {
		t.Fatalf("empty page: %+v %v", empty, err)
	}
	for i := range 123 {
		for _, kind := range []struct {
			source string
			plan   int
		}{{"manual", 0}, {"manual", 7}, {"schedule", 0}} {
			if _, err := storage.WriteValue(t.Context(), m.db, func(tx *sql.Tx) (sql.Result, error) {
				return tx.ExecContext(t.Context(), "INSERT INTO runs(source,plan,targets,recursive,status,created,request_key) VALUES(?,?,'[]',1,'completed','2026-09-26T00:00:00.000Z',?)", kind.source, kind.plan, fmt.Sprintf("%s-%d-%d", kind.source, kind.plan, i))
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[int64]bool{}
	for page := 1; page <= 3; page++ {
		result, err := m.ListPage(t.Context(), ListPageOptions{Category: "manual", Task: "", Offset: max(0, (page)-1) * (50), Limit: 50})
		if err != nil || result.Total != 123 || result.Offset != (page-1)*50 || len(result.Items) != min(50, 123-(page-1)*50) {
			t.Fatalf("page %d: %+v %v", page, result, err)
		}
		for _, run := range result.Items {
			if seen[run.ID] || run.Source != "manual" || run.Plan != 0 {
				t.Fatalf("duplicate or wrong category: %+v", run)
			}
			seen[run.ID] = true
		}
	}
	for _, size := range []int{50, 100, 200} {
		result, err := m.ListPage(t.Context(), ListPageOptions{Category: "automatic", Task: "", Offset: max(0, (1)-1) * (size), Limit: size})
		if err != nil || result.Total != 246 || len(result.Items) != size {
			t.Fatalf("automatic size %d: %+v %v", size, result, err)
		}
	}
	last, err := m.ListPage(t.Context(), ListPageOptions{Category: "manual", Task: "", Offset: 199600, Limit: 200})
	if err != nil || last.Offset != 0 || len(last.Items) != 123 {
		t.Fatalf("clamped page: %+v %v", last, err)
	}
	if _, err := m.ListPage(t.Context(), ListPageOptions{Category: "unknown", Task: "", Offset: 0, Limit: 50}); err == nil {
		t.Fatal("invalid category accepted")
	}
	if _, err := m.ListPage(t.Context(), ListPageOptions{Category: "all", Task: "unknown", Offset: 0, Limit: 50}); err == nil {
		t.Fatal("invalid task accepted")
	}
	if _, err := storage.WriteValue(t.Context(), m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), "UPDATE runs SET task='extract' WHERE id%2=0")
	}); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []struct {
		category, task string
		total          int
	}{{"all", "", 369}, {"all", "extract", 184}, {"all", "rename", 185}, {"manual", "extract", 61}, {"automatic", "extract", 123}} {
		t.Run(filter.category+"/"+filter.task, func(t *testing.T) {
			seen := map[int64]bool{}
			for page := range (filter.total + 49) / 50 {
				result, err := m.ListPage(t.Context(), ListPageOptions{Category: filter.category, Task: filter.task, Offset: max(0, (page+1)-1) * (50), Limit: 50})
				if err != nil || result.Total != filter.total || result.Offset != page*50 {
					t.Fatalf("filtered page: %+v %v", result, err)
				}
				for _, run := range result.Items {
					if seen[run.ID] || filter.task != "" && run.Task != filter.task {
						t.Fatalf("duplicate or wrong task: %+v", run)
					}
					seen[run.ID] = true
				}
			}
			if len(seen) != filter.total {
				t.Fatalf("got %d unique runs, want %d", len(seen), filter.total)
			}
		})
	}
}

func TestRunPageCombinedFiltersAndDurationBounds(t *testing.T) {
	m, _ := setup(t)
	for i, seconds := range []int{59, 60, 299, 300, 1799, 1800, 3599, 3600} {
		_, err := storage.WriteValue(t.Context(), m.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(t.Context(), `INSERT INTO runs(source,targets,recursive,status,created,started,finished,request_key,model_config_id,model_config_title) VALUES('manual','[]',1,'partial_failure','2026-09-25T23:00:00.000Z','2026-09-26T00:00:00.000Z',strftime('%Y-%m-%dT%H:%M:%fZ','2026-09-26T00:00:00.000Z',?),?,42,'同一模型的配置 A')`, fmt.Sprintf("+%d seconds", seconds), fmt.Sprintf("bounds-%d", i))
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// A queued batch has no elapsed duration, even if its submission is old.
	if _, err := storage.WriteValue(t.Context(), m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(source,targets,recursive,status,created,started,finished,request_key,model_config_id) VALUES('manual','[]',1,'partial_failure','2026-09-26T00:00:00.000Z','2026-09-26T00:00:00.900Z','2026-09-26T00:01:00.100Z','fractional',42)`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.WriteValue(t.Context(), m.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(source,targets,recursive,status,created,request_key,model_config_id) VALUES('manual','[]',1,'queued','2026-09-26T00:00:00.000Z','queued',43)`)
	}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		duration string
		total    int
	}{{"under1", 2}, {"1to5", 2}, {"5to30", 2}, {"30to60", 2}, {"over60", 1}} {
		q := url.Values{"status": {"partial_failure"}, "profile": {"42"}, "from": {"2026-09-26T08:00:00+08:00"}, "to": {"2026-09-27T00:00:00.000Z"}, "duration": {tt.duration}}
		page, err := m.ListPage(t.Context(), ListPageOptions{Category: "manual", Task: "rename", Offset: 4900, Limit: 50, Filters: q})
		if err != nil || page.Total != tt.total || len(page.Items) != tt.total || page.Offset != 0 {
			t.Fatalf("%s: %+v %v", tt.duration, page, err)
		}
		q.Set("profile", "43")
		page, err = m.ListPage(t.Context(), ListPageOptions{Category: "all", Task: "", Offset: 0, Limit: 50, Filters: q})
		if err != nil || page.Total != 0 {
			t.Fatalf("configuration filter: %+v %v", page, err)
		}
	}
	for _, q := range []url.Values{{"status": {"success"}}, {"profile": {"abc"}}, {"duration": {"invalid"}}, {"from": {"invalid"}}, {"from": {"2026-09-27T00:00:00.000Z"}, "to": {"2026-09-26T00:00:00.000Z"}}} {
		if _, err := m.ListPage(t.Context(), ListPageOptions{Category: "all", Task: "", Offset: 0, Limit: 50, Filters: q}); err == nil {
			t.Fatalf("invalid filters accepted: %v", q)
		}
	}
	page, err := m.ListPage(t.Context(), ListPageOptions{Category: "all", Task: "", Offset: 0, Limit: 50, Filters: url.Values{"to": {"2026-09-26T00:00:00.000Z"}}})
	if err != nil || page.Total != 0 {
		t.Fatalf("exclusive end: %+v %v", page, err)
	}
}
