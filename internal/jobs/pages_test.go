package jobs

import (
	"fmt"
	"testing"
)

func TestRunPagesFilterBeforePagination(t *testing.T) {
	m, _ := setup(t)
	empty, err := m.ListPage(t.Context(), "manual", 99, 50)
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
		result, err := m.ListPage(t.Context(), "manual", page, 50)
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
		result, err := m.ListPage(t.Context(), "automatic", 1, size)
		if err != nil || result.Total != 246 || len(result.Items) != size {
			t.Fatalf("automatic size %d: %+v %v", size, result, err)
		}
	}
	last, err := m.ListPage(t.Context(), "manual", 999, 200)
	if err != nil || last.Page != 1 || len(last.Items) != 123 {
		t.Fatalf("clamped page: %+v %v", last, err)
	}
	if _, err := m.ListPage(t.Context(), "unknown", 1, 50); err == nil {
		t.Fatal("invalid category accepted")
	}
}
