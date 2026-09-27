//go:build scale

package library

import (
	"os"
	"testing"
	"time"

	"video-rename/internal/storage"
)

// Synthetic index and history, not real CIFS files or a production database.
func TestScaleLargeLibraryProgress(t *testing.T) {
	l, _, path := fixture(t)
	tx, err := l.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	queries := []string{
		`INSERT INTO entries(id,parent,name,kind,identity,size,mtime,observed) VALUES(2,1,'disk-a','directory','disk-a',0,'now','now'),(3,1,'disk-b','directory','disk-b',0,'now','now')`,
		`WITH RECURSIVE n(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM n WHERE x<299) INSERT INTO entries(id,parent,name,kind,identity,size,mtime,observed) SELECT 10+x,2+x/150,'folder-'||x,'directory','folder-'||x,0,'now','now' FROM n`,
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<300000) INSERT INTO entries(id,parent,name,kind,identity,size,mtime,observed) SELECT 1000+x,10+(x-1)/1000,'video-'||x||'.mp4','file','file-'||x,100,'now','now' FROM n`,
		`INSERT INTO marks SELECT identity,size,mtime,'marked',NULL FROM entries WHERE kind='file' AND id%2=0`,
		`INSERT INTO directory_state(dir,generation,refreshed) SELECT id,1,? FROM entries WHERE kind='directory'`,
		`INSERT INTO directory_items SELECT parent,1,id,name,kind,size,mtime FROM entries WHERE id<>1`,
		`INSERT INTO runs(id,source,targets,recursive,status,created,request_key) VALUES(1,'manual','[1]',1,'completed','now','scale-old'),(2,'manual','[2]',1,'running','now','scale-active')`,
		`INSERT INTO run_items(run,entry,identity,size,mtime,status) SELECT 1,id,identity,size,mtime,'done' FROM entries WHERE kind='file'`,
		`INSERT INTO run_items(run,entry,identity,size,mtime,status) SELECT 2,id,identity,size,mtime,'running' FROM entries WHERE kind='file' AND parent<160 AND id%100=0`,
		`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<12000) INSERT INTO events(category,action,message,created,raw_response) SELECT 'success','automatic','history','now',printf('%.*c',65536,'x') FROM n`,
	}
	for _, query := range queries {
		var args []any
		if query == queries[4] {
			args = []any{storage.Now()}
		}
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("300,000 files, 303 directories, 301,500 run items, 12,000 history records; database %.1f MiB", float64(info.Size())/(1<<20))
	entries := []Entry{{ID: 1, Kind: "directory"}, {ID: 2, Kind: "directory"}, {ID: 3, Kind: "directory"}}
	for id := int64(10); id < 30; id++ {
		entries = append(entries, Entry{ID: id, Kind: "directory"})
	}
	for i := range 3 {
		start := time.Now()
		got, err := l.WithProgressBatch(t.Context(), entries)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("directory statistics request %d: %s", i+1, time.Since(start))
		if p := got[0].Progress; p.Total != 300000 || p.Processed != 150000 || p.Active != 1500 || p.Unknown != 0 {
			t.Fatalf("root: %+v", p)
		}
		if p := got[1].Progress; p.Total != 150000 || p.Processed != 75000 || p.Active != 1500 {
			t.Fatalf("disk: %+v", p)
		}
		if c := got[0].Contents; c == nil || c.Directories != 2 {
			t.Fatalf("root contents: %+v", c)
		}
		if c := got[3].Contents; c == nil || c.Videos != 1000 {
			t.Fatalf("folder contents: %+v", c)
		}
	}
}
