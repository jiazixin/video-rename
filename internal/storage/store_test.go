package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
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
	if err = upgraded.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 13 {
		t.Fatal(version, err)
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
	var title, body, settings, key string
	var version, selected int
	if err = upgraded.QueryRow("SELECT title,body,version,is_default,settings,api_key FROM model_configs WHERE id=17").Scan(&title, &body, &version, &selected, &settings, &key); err != nil {
		t.Fatal(err)
	}
	if title != "原提示词" || body != "不要丢失正文" || version != 4 || selected != 1 || settings != "{}" || key != "" {
		t.Fatal("migration changed user data")
	}
	var id int
	if err = upgraded.QueryRow("SELECT model_config_id FROM schedules").Scan(&id); err != nil || id != 17 {
		t.Fatal("lost plan reference", id, err)
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
	if _, err = db.Exec("INSERT INTO model_configs(title,body,created,updated,api_key) VALUES('x','y','now','now',?)", sealed); err != nil {
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
