package storage

import (
	"database/sql"
	"fmt"
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
	if _, err = db.Exec("INSERT INTO prompts(id,title,body,created,updated) VALUES(1,'x','body','now','now'); INSERT INTO model_configs(title,prompt_id,created,updated,api_key) VALUES('x',1,'now','now',?)", sealed); err != nil {
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

func TestCurrentSchemaReopensWithoutChangingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO prompts(id,title,body,created,updated) VALUES(1,'标题','正文','now','now');
 INSERT INTO model_configs(id,title,prompt_id,created,updated) VALUES(1,'模型',1,'now','now');
 INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next,task,model_config_id) VALUES('抽帧','[1]',1,'daily','0 2 * * *','UTC',0,'later','extract',0)`); err != nil {
		t.Fatal(err)
	}
	sealed, err := db.Seal("secret")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if plain, err := db.Unseal(sealed); err != nil || plain != "secret" {
		t.Fatal(plain, err)
	}
	var body, task string
	if err = db.QueryRow("SELECT p.body FROM model_configs c JOIN prompts p ON p.id=c.prompt_id").Scan(&body); err != nil || body != "正文" {
		t.Fatal(body, err)
	}
	if err = db.QueryRow("SELECT task FROM schedules").Scan(&task); err != nil || task != "extract" {
		t.Fatal(task, err)
	}
	if _, err = db.Exec("DELETE FROM prompts WHERE id=1"); err == nil {
		t.Fatal("deleted referenced prompt")
	}
	if _, err = db.Exec("UPDATE model_configs SET prompt_id=999 WHERE id=1"); err == nil {
		t.Fatal("accepted missing prompt")
	}
}

func TestUnsupportedDatabaseRemainsIntact(t *testing.T) {
	for _, version := range []int{0, 1, 21, 20260927} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsupported.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("CREATE TABLE old_data(value TEXT); INSERT INTO old_data VALUES('keep'); PRAGMA user_version=" + fmt.Sprint(version)); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if s, err := Open(path); err == nil {
				s.Close()
				t.Fatal("accepted unsupported schema")
			}
			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var journal string
			if err = db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
				t.Fatal("unsupported database journal mode changed", journal, err)
			}
			var value string
			if err = db.QueryRow("SELECT value FROM old_data").Scan(&value); err != nil || value != "keep" {
				t.Fatal(value, err)
			}
			var tables int
			if err = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables); err != nil || tables != 1 {
				t.Fatal(tables, err)
			}
		})
	}
}

func TestForeignKeysSurviveConnectionReplacement(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "space # and ?.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxIdleConns(0)
	var enabled int
	if err = db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal("foreign keys lost on reconnect", enabled, err)
	}
	if _, err = db.Exec("INSERT INTO model_configs(title,prompt_id,created,updated) VALUES('x',999,'now','now')"); err == nil {
		t.Fatal("missing reference accepted on new connection")
	}
}

func TestDeletedEntityIDsAreNeverReused(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []struct{ name, insert string }{
		{"prompts", `INSERT INTO prompts(title,body,created,updated) VALUES('p','b','now','now')`},
		{"model_configs", `INSERT INTO model_configs(title,prompt_id,created,updated) VALUES('m',(SELECT max(id) FROM prompts),'now','now')`},
		{"schedules", `INSERT INTO schedules(name,targets,recursive,kind,expression,timezone,enabled,next) VALUES('s','[1]',1,'daily','0 2 * * *','UTC',0,'later')`},
		{"extraction_records", `INSERT INTO extraction_records(path,source,status,started) VALUES('x','manual','success','now')`},
	} {
		first, err := db.Exec(table.insert)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := first.LastInsertId()
		if _, err = db.Exec("DELETE FROM "+table.name+" WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		second, err := db.Exec(table.insert)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := second.LastInsertId()
		if next <= id {
			t.Fatalf("%s reused ID %d", table.name, next)
		}
	}
}
