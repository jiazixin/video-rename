package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExclusiveInstanceAndForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if b, e := Open(path); e == nil {
		b.Close()
		t.Fatal("second writer instance allowed")
	}
	var mode string
	if err = a.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatal(mode, err)
	}
	a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}

func TestVersionOneUpgradePreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next) VALUES('保留计划','[1]',1,'daily','0 2 * * *','Asia/Shanghai',1,'2026-09-21T02:00:00+08:00')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var name string
	var promptID, version int
	if err = upgraded.QueryRow("SELECT name,model_config_id FROM schedules").Scan(&name, &promptID); err != nil || name != "保留计划" || promptID != 1 {
		t.Fatal(name, promptID, err)
	}
	if err = upgraded.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 19 {
		t.Fatal(version, err)
	}
}

func TestVersionFifteenBackfillsLegacyUsageLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v14.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema + `
 ALTER TABLE runs ADD COLUMN started TEXT NOT NULL DEFAULT '';
 ALTER TABLE runs ADD COLUMN model_snapshot TEXT NOT NULL DEFAULT '';
 CREATE TABLE model_configs(id INTEGER PRIMARY KEY,title TEXT NOT NULL,body TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,created TEXT NOT NULL,updated TEXT NOT NULL,is_default INTEGER NOT NULL DEFAULT 0,settings TEXT NOT NULL DEFAULT '{}',api_key TEXT NOT NULL DEFAULT '');
 CREATE TABLE notification_channels(kind TEXT PRIMARY KEY,enabled INTEGER NOT NULL DEFAULT 0,failure_only INTEGER NOT NULL DEFAULT 0,credentials TEXT NOT NULL,after_run INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE system_settings(id INTEGER PRIMARY KEY CHECK(id=1),extract_workers INTEGER NOT NULL,cache_gb INTEGER NOT NULL);
 CREATE TABLE image_numberings(id TEXT PRIMARY KEY,parent INTEGER NOT NULL REFERENCES entries(id),status TEXT NOT NULL,created TEXT NOT NULL,fingerprint TEXT NOT NULL,undo_of TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '');
 CREATE TABLE image_numbering_items(batch TEXT NOT NULL REFERENCES image_numberings(id),position INTEGER NOT NULL,entry INTEGER NOT NULL REFERENCES entries(id),version INTEGER NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime TEXT NOT NULL,old_name TEXT NOT NULL,new_name TEXT NOT NULL,temp_name TEXT NOT NULL,current_name TEXT NOT NULL,pending_name TEXT NOT NULL DEFAULT '',operation INTEGER NOT NULL DEFAULT 0,undo_of INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(batch,entry));
 INSERT INTO schema_migrations VALUES(14,'then');
 INSERT INTO runs(id,source,targets,recursive,status,created,started,finished,request_key) VALUES
  (11,'manual','[]',1,'completed','2026-09-01T08:00:00Z','2026-09-01T08:05:00Z','2026-09-01T09:00:00Z','k11'),
  (12,'schedule','[]',1,'completed','2026-09-02T08:00:00Z','','2026-09-02T09:00:00Z','k12');
 INSERT INTO run_token_usage(run,token_count,token_records,token_estimated_records) VALUES
  (11,195,2,1),(12,0,0,0);
 `); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()
	var count int
	if err = upgraded.QueryRow("SELECT count(*) FROM token_usage_records").Scan(&count); err != nil || count != 1 {
		t.Fatal("expected one backfilled row", count, err)
	}
	var run, tokens, records, estimated, legacy int
	var at int64
	if err = upgraded.QueryRow("SELECT run,recorded_at,tokens,records,estimated_records,legacy FROM token_usage_records").Scan(&run, &at, &tokens, &records, &estimated, &legacy); err != nil {
		t.Fatal(err)
	}
	if run != 11 || at != int64(time.Date(2026, 9, 1, 8, 5, 0, 0, time.UTC).UnixMilli()) || tokens != 195 || records != 2 || estimated != 1 || legacy != 1 {
		t.Fatal("backfill mismatch", run, at, tokens, records, estimated, legacy)
	}
	upgraded.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.QueryRow("SELECT count(*) FROM token_usage_records").Scan(&count); err != nil || count != 1 {
		t.Fatal("re-open duplicated legacy rows", count, err)
	}
}

func TestVersionThreeModelsPreservePromptAndPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema + `
 CREATE TABLE prompts(id INTEGER PRIMARY KEY,title TEXT NOT NULL,body TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,created TEXT NOT NULL,updated TEXT NOT NULL,is_default INTEGER NOT NULL DEFAULT 0);
 CREATE UNIQUE INDEX prompt_single_default ON prompts(is_default) WHERE is_default=1;
 ALTER TABLE runs ADD COLUMN prompt_id INTEGER NOT NULL DEFAULT 1;
 ALTER TABLE runs ADD COLUMN prompt_title TEXT NOT NULL DEFAULT '';
 ALTER TABLE runs ADD COLUMN prompt_body TEXT NOT NULL DEFAULT '';
 ALTER TABLE schedules ADD COLUMN prompt_id INTEGER NOT NULL DEFAULT 1;
 INSERT INTO schema_migrations VALUES(2,'then'),(3,'then');
 INSERT INTO prompts VALUES(17,'原提示词','不要丢失正文',4,'then','then',1);
 INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next,prompt_id) VALUES('既有计划','[1]',1,'daily','0 2 * * *','Asia/Shanghai',0,'later',17);
 `); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var title, promptTitle, body, settings, key string
	var version, selected, promptID int
	if err = upgraded.QueryRow("SELECT c.title,p.title,p.body,c.prompt_id,c.version,c.is_default,c.settings,c.api_key FROM model_configs c LEFT JOIN prompts p ON p.id=c.prompt_id WHERE c.id=17").Scan(&title, &promptTitle, &body, &promptID, &version, &selected, &settings, &key); err != nil {
		t.Fatal(err)
	}
	if title != "原提示词" || promptTitle != "原提示词" || body != "不要丢失正文" || promptID == 0 || version != 4 || selected != 1 || settings != "{}" || key != "" {
		t.Fatal("migration changed user data")
	}
	var id int
	if err = upgraded.QueryRow("SELECT model_config_id FROM schedules").Scan(&id); err != nil || id != 17 {
		t.Fatal("lost plan reference", id, err)
	}
}

func TestVersionEighteenBackfillsPromptLibrary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v17.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema + `
 CREATE TABLE notification_channels(kind TEXT PRIMARY KEY,enabled INTEGER NOT NULL DEFAULT 0,failure_only INTEGER NOT NULL DEFAULT 0,credentials TEXT NOT NULL,after_run INTEGER NOT NULL DEFAULT 0);
 ALTER TABLE runs ADD COLUMN model_snapshot TEXT NOT NULL DEFAULT '';
 CREATE TABLE model_configs(id INTEGER PRIMARY KEY,title TEXT NOT NULL,body TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,created TEXT NOT NULL,updated TEXT NOT NULL,is_default INTEGER NOT NULL DEFAULT 0,settings TEXT NOT NULL DEFAULT '{}',api_key TEXT NOT NULL DEFAULT '');
 CREATE UNIQUE INDEX prompt_single_default ON model_configs(is_default) WHERE is_default=1;
 CREATE TABLE system_settings(id INTEGER PRIMARY KEY CHECK(id=1),extract_workers INTEGER NOT NULL,cache_gb INTEGER NOT NULL,stability_seconds INTEGER NOT NULL DEFAULT 30,numbering TEXT NOT NULL DEFAULT '');
 INSERT INTO system_settings(id,extract_workers,cache_gb) VALUES(1,8,10);
 INSERT INTO schema_migrations VALUES(17,'then');
 INSERT INTO model_configs(id,title,body,created,updated,is_default) VALUES
  (1,'共享甲','正文一','then','then',1),
  (2,'共享乙','正文一','then','then',0),
  (3,'独立','正文二','then','then',0);
 `); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var prompts int
	if err = upgraded.QueryRow("SELECT count(*) FROM prompts").Scan(&prompts); err != nil || prompts != 2 {
		t.Fatal("expected one prompt per distinct body", prompts, err)
	}
	var shared, distinct int64
	var sharedTitle string
	if err = upgraded.QueryRow("SELECT prompt_id FROM model_configs WHERE id=1").Scan(&shared); err != nil {
		t.Fatal(err)
	}
	if err = upgraded.QueryRow("SELECT prompt_id FROM model_configs WHERE id=2").Scan(&distinct); err != nil || distinct != shared {
		t.Fatal("identical bodies were not deduplicated", shared, distinct, err)
	}
	if err = upgraded.QueryRow("SELECT prompt_id FROM model_configs WHERE id=3").Scan(&distinct); err != nil || distinct == shared || distinct == 0 {
		t.Fatal("distinct body not mapped", shared, distinct, err)
	}
	if err = upgraded.QueryRow("SELECT title FROM prompts WHERE id=?", shared).Scan(&sharedTitle); err != nil || sharedTitle != "共享甲" {
		t.Fatal("prompt title did not follow the lowest-id config", sharedTitle, err)
	}
	var column string
	found := false
	rows, err := upgraded.Query("PRAGMA table_info(model_configs)")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		if err = rows.Scan(new(any), &column, new(any), new(any), new(any), new(any)); err != nil {
			t.Fatal(err)
		}
		found = found || column == "body"
	}
	rows.Close()
	if found {
		t.Fatal("body column survived the split")
	}
	var fixed string
	if err = upgraded.QueryRow("SELECT fixed_prompt FROM system_settings WHERE id=1").Scan(&fixed); err != nil || fixed != "" {
		t.Fatal(fixed, err)
	}
	upgraded.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.QueryRow("SELECT count(*) FROM prompts").Scan(&prompts); err != nil || prompts != 2 {
		t.Fatal("re-open duplicated prompt rows", prompts, err)
	}
}

func TestMissingEncryptionKeyNeverSilentlyReplacesCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := db.Seal("credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO model_configs(title,created,updated,api_key) VALUES('x','now','now',?)", sealed); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = os.Remove(path + ".key"); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(path); err == nil {
		reopened.Close()
		t.Fatal("generated replacement key for encrypted data")
	}
}
