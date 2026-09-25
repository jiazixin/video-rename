package jobs

import (
	"fmt"
	"testing"
)

func TestRunPagesFilterBeforePagination(t *testing.T) {
	m, _ := setup(t)
	empty, err := m.ListPage(t.Context(), "manual", "", 99, 50)
	if err != nil || empty.Total != 0 || empty.Page != 1 || empty.Items == nil {
		t.Fatalf("empty page: %+v %v", empty, err)
	}
	for i := range 123 {
		for _, kind := range []struct {
			source string
			plan   int
		}{{"manual", 0}, {"manual", 7}, {"schedule", 0}} {
			if _, err := m.DB.Exec("INSERT INTO runs(source,plan,targets,recursive,status,created,request_key) VALUES(?,?,'[]',1,'completed','2026-09-26T00:00:00Z',?)", kind.source, kind.plan, fmt.Sprintf("%s-%d-%d", kind.source, kind.plan, i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[int64]bool{}
	for page := 1; page <= 3; page++ {
		result, err := m.ListPage(t.Context(), "manual", "", page, 50)
		if err != nil || result.Total != 123 || result.Page != page || len(result.Items) != min(50, 123-(page-1)*50) {
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
		result, err := m.ListPage(t.Context(), "automatic", "", 1, size)
		if err != nil || result.Total != 246 || len(result.Items) != size {
			t.Fatalf("automatic size %d: %+v %v", size, result, err)
		}
	}
	last, err := m.ListPage(t.Context(), "manual", "", 999, 200)
	if err != nil || last.Page != 1 || len(last.Items) != 123 {
		t.Fatalf("clamped page: %+v %v", last, err)
	}
	if _, err := m.ListPage(t.Context(), "unknown", "", 1, 50); err == nil {
		t.Fatal("invalid category accepted")
	}
	if _, err := m.ListPage(t.Context(), "all", "unknown", 1, 50); err == nil {
		t.Fatal("invalid task accepted")
	}
	if _, err := m.DB.Exec("UPDATE runs SET task='extract' WHERE id%2=0"); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []struct {
		category, task string
		total          int
	}{{"all", "", 369}, {"all", "extract", 184}, {"all", "rename", 185}, {"manual", "extract", 61}, {"automatic", "extract", 123}} {
		t.Run(filter.category+"/"+filter.task, func(t *testing.T) {
			seen := map[int64]bool{}
			for page := range (filter.total + 49) / 50 {
				result, err := m.ListPage(t.Context(), filter.category, filter.task, page+1, 50)
				if err != nil || result.Total != filter.total || result.Page != page+1 {
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
