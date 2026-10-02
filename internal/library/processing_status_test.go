package library

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"video-rename/internal/domain"
)

func setTestProcessingSource(t *testing.T, l *Library, parent int64, name, source string) {
	t.Helper()
	result, err := l.DB.Exec(`INSERT INTO marks(identity,size,mtime,source) SELECT identity,size,mtime,? FROM entries WHERE parent=? AND name=? AND present=1`, source, parent, name)
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
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, l, 1)
	sources := []string{"", "marked", "manual", "automatic", "restore"}
	for i := range 255 {
		if source := sources[i%len(sources)]; source != "" {
			setTestProcessingSource(t, l, 1, fmt.Sprintf("clip%d.mp4", i), source)
		}
	}
	// A mark from an older file snapshot must not count as the current state.
	if _, err := l.DB.Exec(`UPDATE marks SET size=size+1 WHERE identity=(SELECT identity FROM entries WHERE name='clip254.mp4')`); err != nil {
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
				directories := []string{"folder2", "folder10"}
				if direction == "desc" {
					slices.Reverse(files)
					slices.Reverse(directories)
				}
				expected := append(directories, files...)
				var actual []string
				cursor := 0
				var generation int64
				for page := 1; ; page++ {
					p, err := l.ListPageFiltered(t.Context(), 1, page, 50, "", "video", status, "status_"+direction)
					if err != nil || p.Number != page || p.Total != len(expected) {
						t.Fatalf("page %d: %+v, %v", page, p, err)
					}
					c, err := l.ListOrderedFiltered(t.Context(), 1, cursor, 50, generation, "", "video", status, false, "status_"+direction)
					if err != nil || c.Total != p.Total || c.Next != p.Next || !slices.EqualFunc(c.Items, p.Items, func(a, b Entry) bool { return a.ID == b.ID }) {
						t.Fatalf("cursor disagrees with page %d: %v", page, err)
					}
					for _, e := range p.Items {
						actual = append(actual, e.Name)
					}
					if p.Next == 0 {
						break
					}
					cursor, generation = p.Next, p.Generation
				}
				if !slices.Equal(actual, expected) {
					t.Fatalf("wrong filtered order: got %v, want %v", actual, expected)
				}
			})
		}
	}
	p, err := l.ListPageFiltered(t.Context(), 1, 99, 50, "clip12", "video", "manual", "status_asc")
	if err != nil || p.Number != 1 || p.Total != 3 || len(p.Items) != 3 || p.Items[0].Name != "clip12.mp4" || p.Items[1].Name != "clip122.mp4" || p.Items[2].Name != "clip127.mp4" {
		t.Fatalf("combined filters and clamping: %+v, %v", p, err)
	}
	p, err = l.ListPageFiltered(t.Context(), 1, 1, 50, "", "image", "processed", "status_asc")
	if err != nil || p.Total != 2 || len(p.Items) != 2 || p.Items[0].Kind != "directory" || p.Items[1].Kind != "directory" {
		t.Fatalf("media filter or directory navigation lost: %+v, %v", p, err)
	}
	if _, err := l.ListPageFiltered(t.Context(), 1, 1, 50, "", "", "invalid' OR 1=1", "status_asc"); err == nil {
		t.Fatal("invalid status accepted")
	}
}

func TestProcessingStatusUsesLiveTaskPrecedenceAndScope(t *testing.T) {
	l, root, _ := fixture(t)
	if err := os.Mkdir(filepath.Join(root, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "child", "clip.mp4"))
	refresh(t, l, 1)
	dir := entry(t, l, 1, "child")
	refresh(t, l, dir.ID)
	file := entry(t, l, dir.ID, "clip.mp4")
	setTestProcessingSource(t, l, dir.ID, file.Name, "manual")
	if _, err := l.DB.Exec(`INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[1]',1,'scanning','now','status-scope')`); err != nil {
		t.Fatal(err)
	}
	assertStatus := func(filter, task string, count int) {
		t.Helper()
		p, err := l.ListPageFiltered(t.Context(), dir.ID, 1, 50, "", "video", filter, "status_asc")
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
	if _, err := l.DB.Exec(`INSERT INTO run_items(run,entry,identity,size,mtime,status) VALUES(1,?,?,?,?, 'running')`, file.ID, file.Identity, file.Size, file.Mtime); err != nil {
		t.Fatal(err)
	}
	assertStatus("running", "running", 1)
	if _, err := l.DB.Exec(`UPDATE runs SET status='paused' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	assertStatus("active", "paused", 1)
	assertStatus("paused", "paused", 1)
	assertStatus("running", "", 0)
	if _, err := l.DB.Exec(`UPDATE run_items SET status='done' WHERE run=1`); err != nil {
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
	setTestProcessingSource(t, l, 1, "2.jpg", "marked")
	setTestProcessingSource(t, l, 1, "5.jpg", "automatic")
	setTestProcessingSource(t, l, 1, "photo2.jpg", "manual")
	setTestProcessingSource(t, l, 1, "photo10.jpg", "manual")
	settings := domain.DefaultNumberingSettings()
	settings.Image.Compact = true
	for _, direction := range []string{"asc", "desc"} {
		b, err := l.PreviewNumberingFilteredContext(t.Context(), 1, "image", "", "manual", "status_"+direction, settings)
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
	b, err := l.PreviewNumberingFilteredContext(t.Context(), 1, "image", "", "manual", "status_desc", settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ApplyNumbering(b.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1.jpg", "2.jpg", "3.jpg", "4.jpg", "5.jpg"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("unexpected numbering result: %s: %v", name, err)
		}
	}
	if _, err := l.PreviewNumberingFilteredContext(t.Context(), 1, "image", "", "marked", "status_asc", settings); err == nil || !strings.Contains(err.Error(), "没有可编号") {
		t.Fatalf("protected marked files entered numbering: %v", err)
	}
}
