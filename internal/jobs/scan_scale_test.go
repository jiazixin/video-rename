//go:build scale

package jobs

import (
	"testing"
	"time"

	"video-rename/internal/library"
)

func TestScaleCandidateQueue(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.DB.Exec(`WITH RECURSIVE n(x) AS (VALUES(2) UNION ALL SELECT x+1 FROM n WHERE x<300001)
 INSERT INTO entries(id,parent,name,kind,identity,size,mtime,observed) SELECT x,1,'video-'||x||'.mp4','file','identity-'||x,100,'now','now' FROM n`); err != nil {
		t.Fatal(err)
	}
	id, err := m.Create(Request{Targets: []int64{1}, Recursive: true, Key: "large-queue-test"}, "manual", 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := m.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for after := int64(1); after < 300001; {
		rows, err := m.DB.Read().Query("SELECT id,parent,name,kind,identity,size,mtime FROM entries WHERE parent=? AND present=1 AND id>? ORDER BY id LIMIT 128", 1, after)
		if err != nil {
			t.Fatal(err)
		}
		files := []library.Entry{}
		for rows.Next() {
			var e library.Entry
			if err = rows.Scan(&e.ID, &e.Parent, &e.Name, &e.Kind, &e.Identity, &e.Size, &e.Mtime); err != nil {
				t.Fatal(err)
			}
			if e.ID%2 == 0 {
				e.Processed = "marked"
			}
			files = append(files, e)
			after = e.ID
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if len(files) == 0 {
			t.Fatal("scan stopped early")
		}
		if err = m.queueFiles(t.Context(), batch, files); err != nil {
			t.Fatal(err)
		}
	}
	r, err := m.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if r.Discovered != 300000 || r.Skipped != 150000 {
		t.Fatalf("counters: %+v", r)
	}
	var count int
	if err = m.DB.QueryRow("SELECT COUNT(*) FROM run_items WHERE run=?", id).Scan(&count); err != nil || count != 300000 {
		t.Fatalf("items: %d %v", count, err)
	}
	t.Logf("300,000 indexed candidates queued in bounded transactions: %s (no filesystem scan or media processing)", time.Since(start))
}
