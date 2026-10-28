package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportCacheBoundedAndIsolated(t *testing.T) {
	s := &Store{}
	s.CacheReport("one", 2, []string{"original"})
	var got []string
	if !s.CachedReport("one", 2, &got) {
		t.Fatal("missing cached result")
	}
	got[0] = "changed"
	s.CacheReport("one", 1, []string{"stale snapshot"})
	if !s.CachedReport("one", 2, &got) || got[0] != "original" {
		t.Fatal(got)
	}
	if s.CachedReport("one", 3, &got) {
		t.Fatal("revision mismatch hit cache")
	}
	s.CacheReport("oversized", 1, strings.Repeat("x", reportCacheLimit+1))
	var oversized string
	if s.CachedReport("oversized", 1, &oversized) {
		t.Fatal("oversized report was cached")
	}
	for i := range 100 {
		s.CacheReport(fmt.Sprint(i), 1, strings.Repeat("x", 256<<10))
	}
	if s.reports.bytes > reportCacheLimit || len(s.reports.entries) > 32 {
		t.Fatal(s.reports.bytes, len(s.reports.entries))
	}
}

func TestReportRevisionCommitsAtomically(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "revision.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write, err := s.write.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write.ExecContext(t.Context(), "INSERT INTO runs(source,targets,recursive,status,created,request_key) VALUES('manual','[]',0,'queued','2026-10-05T00:00:00.000Z','revision')"); err != nil {
		t.Fatal(err)
	}
	read, err := s.read.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	before, err := ReportRevision(t.Context(), read, "runs")
	if err != nil || before != 0 {
		t.Fatal(before, err)
	}
	if err = write.Commit(); err != nil {
		t.Fatal(err)
	}
	same, err := ReportRevision(t.Context(), read, "runs")
	if err != nil || same != before {
		t.Fatal(same, err)
	}
	read.Rollback()
	read, err = s.read.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	after, err := ReportRevision(t.Context(), read, "runs")
	if err != nil || after <= before {
		t.Fatal(after, err)
	}
}
