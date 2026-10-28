package library

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
	"video-rename/internal/timefmt"
)

type setTestProcessingSourceOptions struct {
	l      *Library
	parent int64
	name   string
	source string
}

func setTestProcessingSource(t *testing.T, options setTestProcessingSourceOptions) {
	t.Helper()
	l, parent, name, source := options.l, options.parent, options.name, options.source

	result, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO marks(identity,size,mtime_ns,source) SELECT identity,size,mtime_ns,? FROM entries WHERE parent=? AND name=? AND present=1`, source, parent, name)
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("mark %s: %d, %v", name, count, err)
	}
}

func TestProcessingStatusFiltersAndSortingAcrossPages(t *testing.T) {
	l, root, _ := fixture(t)
	for i := range 255 {
		put(t, filepath.Join(root, fmt.Sprintf("clip%d.mp4", i)))
	}
	for _, name := range []string{"folder2", "folder10"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	sources := []string{"", "marked", "manual", "automatic", "restore"}
	for i := range 255 {
		if source := sources[i%len(sources)]; source != "" {
			setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: 1, name: fmt.Sprintf("clip%d.mp4", i), source: source})
		}
	}
	// A mark from an older file snapshot must not count as the current state.
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE marks SET size=size+1 WHERE identity=(SELECT identity FROM entries WHERE name='clip254.mp4')`)
	}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"", "unprocessed", "processed", "marked", "manual", "automatic", "restore", "active"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(status+"/"+direction, func(t *testing.T) {
				// Build the expected status groups independently of the query, with
				// natural numeric names within each group.
				var files []string
				for _, source := range sources {
					for i := range 255 {
						current := sources[i%len(sources)]
						if i == 254 {
							current = ""
						}
						if current != source {
							continue
						}
						matches := status == "" || status == source || (status == "unprocessed" && source == "") || (status == "processed" && (source == "marked" || source == "manual" || source == "automatic"))
						if matches {
							files = append(files, fmt.Sprintf("clip%d.mp4", i))
						}
					}
				}
				var directories []string
				if status == "" || status == "unprocessed" {
					directories = []string{"folder2", "folder10"}
				}
				if direction == "desc" {
					slices.Reverse(files)
					slices.Reverse(directories)
				}
				expected := append(directories, files...)
				var actual []string
				for page := 1; ; page++ {
					p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: max(0, (page)-1) * (50), Limit: 50, Query: "", Kind: "video", Status: status, SortBy: "status_" + direction})
					if err != nil || p.Offset != (page-1)*p.Limit || p.Total != len(expected) {
						t.Fatalf("page %d: %+v, %v", page, p, err)
					}
					for _, e := range p.Items {
						actual = append(actual, e.Name)
					}
					if page*50 >= p.Total {
						break
					}
				}
				if !slices.Equal(actual, expected) {
					t.Fatalf("wrong filtered order: got %v, want %v", actual, expected)
				}
			})
		}
	}
	p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 4900, Limit: 50, Query: "clip12", Kind: "video", Status: "manual", SortBy: "status_asc"})
	if err != nil || p.Offset != 0 || p.Total != 3 || len(p.Items) != 3 || p.Items[0].Name != "clip12.mp4" || p.Items[1].Name != "clip122.mp4" || p.Items[2].Name != "clip127.mp4" {
		t.Fatalf("combined filters and clamping: %+v, %v", p, err)
	}
	p, err = l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 50, Query: "", Kind: "image", Status: "processed", SortBy: "status_asc"})
	if err != nil || p.Total != 0 || len(p.Items) != 0 {
		t.Fatalf("unknown directories incorrectly reported as processed: %+v, %v", p, err)
	}
	if _, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 50, Query: "", Kind: "", Status: "invalid' OR 1=1", SortBy: "status_asc"}); err == nil {
		t.Fatal("invalid status accepted")
	}
}

func TestProcessingStatusUsesLiveTaskPrecedenceAndScope(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.Mkdir(filepath.Join(root, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "child", "clip.mp4"))
	refresh(t, l, 1)
	dir := entry(t, entryOptions{l: l, parent: 1, name: "child"})
	refresh(t, l, dir.ID)
	file := entry(t, entryOptions{l: l, parent: dir.ID, name: "clip.mp4"})
	setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: dir.ID, name: file.Name, source: "manual"})
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[1]',1,'scanning','2026-10-28T00:00:00.000Z','status-scope')`)
	}); err != nil {
		t.Fatal(err)
	}
	assertStatus := func(filter, task string, count int) {
		t.Helper()
		p, err := l.ListPage(t.Context(), ListPageOptions{ID: dir.ID, Offset: 0, Limit: 50, Query: "", Kind: "video", Status: filter, SortBy: "status_asc"})
		if err != nil || p.Total != count || len(p.Items) != count {
			t.Fatalf("filter %s: %+v, %v", filter, p, err)
		}
		if count > 0 && p.Items[0].TaskStatus != task {
			t.Fatalf("filter %s state: %+v", filter, p.Items[0])
		}
	}
	assertStatus("active", "queued", 1)
	assertStatus("queued", "queued", 1)
	assertStatus("processed", "", 0)
	assertStatus("manual", "", 0)
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO run_items(run,entry,identity,size,mtime_ns,status) VALUES(1,?,?,?,?, 'running')`, file.ID, file.Identity, file.Size, file.ModifiedNS)
	}); err != nil {
		t.Fatal(err)
	}
	assertStatus("running", "running", 1)
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE runs SET status='paused' WHERE id=1`)
	}); err != nil {
		t.Fatal(err)
	}
	assertStatus("active", "paused", 1)
	assertStatus("paused", "paused", 1)
	assertStatus("running", "", 0)
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE run_items SET status='done' WHERE run=1`)
	}); err != nil {
		t.Fatal(err)
	}
	assertStatus("active", "", 0)
	assertStatus("manual", "", 1)
	assertStatus("processed", "", 1)
}

func TestNumberingStatusFilterKeepsHiddenNumbersAndUsesStatusSorting(t *testing.T) {
	l, root, _ := fixture(t)
	for _, name := range []string{"1.jpg", "2.jpg", "5.jpg", "photo2.jpg", "photo10.jpg"} {
		put(t, filepath.Join(root, name))
	}
	refresh(t, l, 1)
	setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: 1, name: "2.jpg", source: "marked"})
	setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: 1, name: "5.jpg", source: "automatic"})
	setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: 1, name: "photo2.jpg", source: "manual"})
	setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: 1, name: "photo10.jpg", source: "manual"})
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = true
	for _, direction := range []string{"asc", "desc"} {
		b, err := l.PreviewNumbering(t.Context(), NumberingRequest{Parent: 1, Media: "image", Status: "manual", Sort: "status_" + direction, Settings: settings})
		if err != nil || b.Total != 2 || len(b.Items) != 2 {
			t.Fatalf("filtered numbering: %+v, %v", b, err)
		}
		expected := []string{"photo2.jpg", "photo10.jpg"}
		if direction == "desc" {
			slices.Reverse(expected)
		}
		for i, item := range b.Items {
			if item.Old != expected[i] || item.New != fmt.Sprintf("%d.jpg", i+3) {
				t.Fatalf("hidden number or sort ignored: %+v", b.Items)
			}
		}
	}
	b, err := l.PreviewNumbering(t.Context(), NumberingRequest{Parent: 1, Media: "image", Status: "manual", Sort: "status_desc", Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ApplyNumbering(t.Context(), b.ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1.jpg", "2.jpg", "3.jpg", "4.jpg", "5.jpg"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("unexpected numbering result: %s: %v", name, err)
		}
	}
	if _, err := l.PreviewNumbering(t.Context(), NumberingRequest{Parent: 1, Media: "image", Status: "marked", Sort: "status_asc", Settings: settings}); err == nil || !errors.Is(err, domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonNumberingNothingSelected, Message: ""})) {
		t.Fatalf("protected marked files entered numbering: %v", err)
	}
}

func TestDirectoryProcessingFiltersUseSubtreesBeforePaging(t *testing.T) {
	l, _, _ := fixture(t)
	want := map[string][]string{}
	if err := l.db.Write(t.Context(), func(tx *sql.Tx) error {
		// The index below is synthetic; the media root on disk is empty. Listing a
		// directory refreshed over 30 seconds ago rescans it, and under -race this
		// test runs longer than that, so a rescan would publish the empty disk
		// state mid-test. Stamp the root as refreshed in the future instead.
		refreshed := timefmt.Format(time.Now().Add(24 * time.Hour))
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO directory_state(dir,generation,refreshed) VALUES(1,1,?)", refreshed); err != nil {
			t.Fatal(err)
		}
		for i := range 210 {
			dir := int64(10 + i*4)
			name := fmt.Sprintf("folder%d", i)
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(?,1,?,'directory',?,0,0,0);
 INSERT INTO directory_items(dir,generation,entry,name,kind,size,mtime_ns) VALUES(1,1,?,?,'directory',0,0)`, dir, name, name, dir, name); err != nil {
				t.Fatal(err)
			}
			want[""] = append(want[""], name)
			state := i % 7
			// One seventh is undiscovered; another is a known empty directory.
			if state == 5 {
				want["unprocessed"] = append(want["unprocessed"], name)
				continue
			}
			if _, err := tx.ExecContext(t.Context(), "INSERT INTO directory_state(dir,generation,refreshed) VALUES(?,1,?)", dir, storage.Now()); err != nil {
				t.Fatal(err)
			}
			if state == 6 {
				continue
			}
			// Videos are two levels below each listed directory: testing only direct
			// children would accidentally treat every folder as empty.
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(?,?,'nested','directory',?,0,0,0);
 INSERT INTO directory_state(dir,generation,refreshed) VALUES(?,1,?)`, dir+1, dir, name+"/nested", dir+1, storage.Now()); err != nil {
				t.Fatal(err)
			}
			for j := range 2 {
				identity := fmt.Sprintf("%s/video%d", name, j)
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO entries(id,parent,name,kind,identity,size,mtime_ns,observed) VALUES(?,?,'clip'||?||'.mp4','file',?,1,0,0)`, dir+2+int64(j), dir+1, j, identity); err != nil {
					t.Fatal(err)
				}
				source := ""
				if state < 3 {
					source = []string{"marked", "manual", "automatic"}[state]
				} else if state == 3 && j == 0 {
					source = "manual"
				}
				if source != "" {
					if _, err := tx.ExecContext(t.Context(), `INSERT INTO marks(identity,size,mtime_ns,source) VALUES(?,1,0,?)`, identity, source); err != nil {
						t.Fatal(err)
					}
				}
			}
			if state < 3 {
				want["processed"] = append(want["processed"], name)
				want[[]string{"marked", "manual", "automatic"}[state]] = append(want[[]string{"marked", "manual", "automatic"}[state]], name)
			} else {
				want["unprocessed"] = append(want["unprocessed"], name)
				if state == 3 {
					want["manual"] = append(want["manual"], name)
				}
			}
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "status"} {
		for _, status := range []string{"", "unprocessed", "processed", "marked", "manual", "automatic", "restore", "active"} {
			for _, direction := range []string{"asc", "desc"} {
				t.Run(field+"/"+status+"/"+direction, func(t *testing.T) {
					expected := slices.Clone(want[status])
					if field == "status" {
						// Independent fixture grouping: unknown, empty, unprocessed,
						// partial, then all three fully processed source groups.
						group := func(name string) int {
							var index int
							if _, err := fmt.Sscanf(name, "folder%d", &index); err != nil {
								t.Fatal(err)
							}
							return []int{4, 4, 4, 3, 2, 0, 1}[index%7]
						}
						slices.SortStableFunc(expected, func(a, b string) int { return group(a) - group(b) })
					}
					if direction == "desc" {
						slices.Reverse(expected)
					}
					var actual []string
					for page := 1; ; page++ {
						p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: max(0, (page)-1) * (50), Limit: 50, Query: "", Kind: "video", Status: status, SortBy: field + "_" + direction})
						if err != nil || p.Total != len(expected) {
							t.Fatalf("page %d: total %d, want %d, error %v", page, p.Total, len(expected), err)
						}
						for _, e := range p.Items {
							actual = append(actual, e.Name)
						}
						if page*50 >= p.Total {
							break
						}
					}
					if !slices.Equal(actual, expected) {
						t.Fatalf("filtered directories: got %v, want %v", actual, expected)
					}
				})
			}
		}
	}
	// Filtering removes later pages before clamping, and combines with search.
	p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 4950, Limit: 50, Query: "folder20", Kind: "image", Status: "unprocessed", SortBy: "name_asc"})
	if err != nil || p.Offset != 0 || p.Total != 5 {
		t.Fatalf("search/media/clamping: page %d, total %d, error %v", p.Offset, p.Total, err)
	}
}

func TestDirectoryFiltersUseLiveTaskStateAndUnknownDescendants(t *testing.T) {
	l, root, _ := fixture(t)
	for _, dir := range []string{"completed", "unknown/nested"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(root, "completed", "clip.mp4"))
	put(t, filepath.Join(root, "unknown", "clip.mp4"))
	refresh(t, l, 1)
	completed, unknown := entry(t, entryOptions{l: l, parent: 1, name: "completed"}), entry(t, entryOptions{l: l, parent: 1, name: "unknown"})
	for _, dir := range []Entry{completed, unknown} {
		refresh(t, l, dir.ID)
		setTestProcessingSource(t, setTestProcessingSourceOptions{l: l, parent: dir.ID, name: "clip.mp4", source: "manual"})
	}
	assertNames := func(status string, expected ...string) {
		t.Helper()
		p, err := l.ListPage(t.Context(), ListPageOptions{ID: 1, Offset: 0, Limit: 50, Query: "", Kind: "", Status: status, SortBy: "name_asc"})
		if err != nil || p.Total != len(expected) {
			t.Fatalf("%s: total %d, error %v", status, p.Total, err)
		}
		var names []string
		for _, e := range p.Items {
			names = append(names, e.Name)
		}
		if !slices.Equal(names, expected) {
			t.Fatalf("%s: got %v, want %v", status, names, expected)
		}
	}
	assertNames("unprocessed", "unknown")
	assertNames("processed", "completed")
	assertNames("manual", "completed", "unknown")
	file := entry(t, entryOptions{l: l, parent: completed.ID, name: "clip.mp4"})
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual',?,0,'scanning','2026-10-28T00:00:00.000Z','directory-filter')`, fmt.Sprintf("[%d]", file.ID))
	}); err != nil {
		t.Fatal(err)
	}
	assertNames("active", "completed")
	assertNames("queued", "completed")
	assertNames("manual", "unknown")
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE runs SET targets=?,recursive=1,status='paused' WHERE id=1`, fmt.Sprintf("[%d]", completed.ID))
	}); err != nil {
		t.Fatal(err)
	}
	assertNames("paused", "completed")
	assertNames("active", "completed")
	assertNames("processed")
	assertNames("unprocessed", "unknown")
	if _, err := storage.WriteValue(t.Context(), l.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(t.Context(), `UPDATE runs SET status='completed' WHERE id=1; UPDATE marks SET size=size+1 WHERE identity=?`, file.Identity)
	}); err != nil {
		t.Fatal(err)
	}
	assertNames("unprocessed", "completed", "unknown")
	assertNames("processed")
}
