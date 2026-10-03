//go:build scale_large

package library

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This matches a large recursive root with nested folders, using real temporary
// files and a local database. It measures local behavior, not NAS throughput.
// The separate tag keeps 530,000 file creations out of ordinary test runs.
func TestScaleLargeDirectoryScan(t *testing.T) {
	const branches = 10
	const leavesPerBranch = 10
	const filesPerLeaf = 5300
	const totalFiles = branches * leavesPerBranch * filesPerLeaf
	const totalDirectories = 1 + branches + branches*leavesPerBranch
	l, root, _ := fixture(t)
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	created := 0
	start := time.Now()
	contents := []byte("video fixture")
	for branch := range branches {
		for leaf := range leavesPerBranch {
			dir := filepath.Join(root, fmt.Sprintf("branch-%02d", branch), fmt.Sprintf("leaf-%02d", leaf))
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for file := range filesPerLeaf {
				path := filepath.Join(dir, fmt.Sprintf("clip-%05d.mp4", file))
				if err := os.WriteFile(path, contents, 0600); err != nil {
					t.Fatalf("create %s: %v", path, err)
				}
				created++
			}
		}
		t.Logf("creation progress: %d/%d real files, elapsed=%s", created, totalFiles, time.Since(start))
	}
	t.Logf("created %d real files in %d nested directories: %s", created, totalDirectories, time.Since(start))

	generations := make(map[int64]int64, totalDirectories)
	scanRound := func(name string, unchanged bool) {
		t.Helper()
		start := time.Now()
		scanner, err := l.NewDirectoryScanner(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := scanner.Close(); err != nil {
				t.Errorf("close %s scanner: %v", name, err)
			}
		}()
		type directory struct {
			id    int64
			depth int
		}
		pending := []directory{{id: 1}}
		seen := make(map[int64]bool, totalDirectories)
		var files, added, changed, removed int64
		for len(pending) > 0 {
			current := pending[0]
			pending = pending[1:]
			if seen[current.id] {
				t.Fatalf("%s visited directory %d twice", name, current.id)
			}
			seen[current.id] = true
			result, err := scanner.ScanDirectory(t.Context(), current.id)
			if err != nil {
				t.Fatalf("%s directory %d at depth %d: %v", name, current.id, current.depth, err)
			}
			wantFiles, wantChildren := int64(0), 0
			switch current.depth {
			case 0:
				wantChildren = branches
			case 1:
				wantChildren = leavesPerBranch
			case 2:
				wantFiles = filesPerLeaf
			default:
				t.Fatalf("%s unexpectedly reached depth %d", name, current.depth)
			}
			if result.Files != wantFiles || len(result.Directories) != wantChildren {
				t.Fatalf("%s directory %d counts: %+v; want files=%d children=%d", name, current.id, result, wantFiles, wantChildren)
			}
			wantAdded := wantFiles + int64(wantChildren)
			if unchanged {
				wantAdded = 0
			}
			if result.Added != wantAdded || result.Changed != 0 || result.Removed != 0 {
				t.Fatalf("%s directory %d changes: %+v; want added=%d", name, current.id, result, wantAdded)
			}
			state, err := l.DirectoryState(t.Context(), current.id)
			if err != nil || state.Generation == 0 || state.Error != "" || state.Refreshing {
				t.Fatalf("%s directory %d state: %+v %v", name, current.id, state, err)
			}
			if unchanged {
				if before, ok := generations[current.id]; !ok || state.Generation != before {
					t.Fatalf("unchanged directory %d generation: %d -> %d", current.id, before, state.Generation)
				}
			} else {
				generations[current.id] = state.Generation
			}
			for _, child := range result.Directories {
				pending = append(pending, directory{id: child, depth: current.depth + 1})
			}
			files += result.Files
			added += result.Added
			changed += result.Changed
			removed += result.Removed
			if len(seen)%10 == 0 || len(pending) == 0 {
				t.Logf("%s progress: directories=%d/%d files=%d/%d elapsed=%s", name, len(seen), totalDirectories, files, totalFiles, time.Since(start))
			}
		}
		wantAdded := int64(totalFiles + totalDirectories - 1)
		if unchanged {
			wantAdded = 0
		}
		if len(seen) != totalDirectories || files != totalFiles || added != wantAdded || changed != 0 || removed != 0 {
			t.Fatalf("%s totals: directories=%d files=%d added=%d changed=%d removed=%d", name, len(seen), files, added, changed, removed)
		}
		t.Logf("%s finished: directories=%d files=%d added=%d changed=%d removed=%d elapsed=%s", name, len(seen), files, added, changed, removed, time.Since(start))
	}

	scanRound("initial recursive scan", false)
	scanRound("unchanged recursive rescan", true)
	for _, check := range []struct {
		name  string
		query string
		want  int
	}{
		{"indexed files", "SELECT count(*) FROM entries WHERE kind='file' AND present=1", totalFiles},
		{"indexed directories", "SELECT count(*) FROM entries WHERE kind='directory' AND present=1", totalDirectories},
		{"complete directory states", "SELECT count(*) FROM directory_state WHERE generation>0 AND error=''", totalDirectories},
		{"visible directory items", `SELECT count(*) FROM directory_items i JOIN directory_state s ON s.dir=i.dir AND s.generation=i.generation`, totalFiles + totalDirectories - 1},
		{"unpublished directory items", `SELECT count(*) FROM directory_items i LEFT JOIN directory_state s ON s.dir=i.dir AND s.generation=i.generation WHERE s.dir IS NULL`, 0},
		{"invisible candidates", "SELECT count(*) FROM entries WHERE present=0 OR observed LIKE 'directory-scan:%'", 0},
	} {
		var count int
		if err := l.DB.QueryRowContext(t.Context(), check.query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != check.want {
			t.Fatalf("%s=%d, want %d", check.name, count, check.want)
		}
	}
	stages, err := os.ReadDir(scratch)
	if err != nil || len(stages) != 0 {
		t.Fatalf("scan stages were not removed: %v %v", stages, err)
	}
	var integrity string
	if err := l.DB.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("database integrity check: %s", integrity)
	}
	rows, err := l.DB.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("database foreign key check found a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Log("exact recursive counts, unchanged generations, candidate cleanup, database integrity, and foreign keys verified")
}
