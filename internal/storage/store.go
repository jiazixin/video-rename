// Package storage owns the SQLite schema and connection lifecycle.
package storage

import (
	"crypto/cipher"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Store struct {
	*sql.DB
	lock   *os.File
	secret cipher.AEAD
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("数据库已被另一实例使用")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, lock: lock}
	if _, err = db.Exec(schema); err != nil {
		s.Close()
		return nil, err
	}
	if err = s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		s.Close()
		return nil, err
	}
	if err = s.initSecret(path + ".key"); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error {
	err := s.DB.Close()
	if s.lock != nil {
		s.lock.Close()
	}
	return err
}
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
INSERT OR IGNORE INTO schema_migrations VALUES(1,datetime('now'));
CREATE TABLE IF NOT EXISTS library(id INTEGER PRIMARY KEY CHECK(id=1), root TEXT NOT NULL, identity TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS entries(
 id INTEGER PRIMARY KEY, parent INTEGER NOT NULL DEFAULT 0, name TEXT NOT NULL, kind TEXT NOT NULL,
 identity TEXT NOT NULL, size INTEGER NOT NULL, mtime TEXT NOT NULL, present INTEGER NOT NULL DEFAULT 1,
 version INTEGER NOT NULL DEFAULT 1, observed TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS entry_path ON entries(parent,name) WHERE present=1;
CREATE INDEX IF NOT EXISTS entry_identity ON entries(identity);
CREATE TABLE IF NOT EXISTS directory_state(dir INTEGER PRIMARY KEY REFERENCES entries(id), generation INTEGER NOT NULL DEFAULT 0, refreshed TEXT, error TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS directory_items(dir INTEGER NOT NULL REFERENCES entries(id), generation INTEGER NOT NULL, entry INTEGER NOT NULL REFERENCES entries(id), name TEXT NOT NULL, kind TEXT NOT NULL, size INTEGER NOT NULL, mtime TEXT NOT NULL, PRIMARY KEY(dir,generation,entry));
CREATE INDEX IF NOT EXISTS directory_natural ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name COLLATE NATURAL_NAME,entry);
CREATE INDEX IF NOT EXISTS directory_natural_desc ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name COLLATE NATURAL_NAME DESC,entry DESC);
CREATE INDEX IF NOT EXISTS directory_sort ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name,entry);
CREATE INDEX IF NOT EXISTS directory_sort_desc ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name DESC,entry DESC);
CREATE TABLE IF NOT EXISTS marks(identity TEXT NOT NULL, size INTEGER NOT NULL, mtime TEXT NOT NULL, source TEXT NOT NULL, operation INTEGER NOT NULL REFERENCES operations(id), PRIMARY KEY(identity,size,mtime));
CREATE TABLE IF NOT EXISTS operations(
 id INTEGER PRIMARY KEY, entry INTEGER NOT NULL REFERENCES entries(id), parent INTEGER NOT NULL REFERENCES entries(id), old_name TEXT NOT NULL,new_name TEXT NOT NULL,
 identity TEXT NOT NULL,size INTEGER NOT NULL,mtime TEXT NOT NULL,kind TEXT NOT NULL,source TEXT NOT NULL,
 status TEXT NOT NULL,undo_of INTEGER NOT NULL DEFAULT 0,undone INTEGER NOT NULL DEFAULT 0,
 request_key TEXT UNIQUE NOT NULL,created TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',run INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS operation_entry ON operations(entry,id DESC);
CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY,category TEXT NOT NULL,action TEXT NOT NULL,entry INTEGER NOT NULL DEFAULT 0,operation INTEGER NOT NULL DEFAULT 0,run INTEGER NOT NULL DEFAULT 0,old_path TEXT NOT NULL DEFAULT '',new_path TEXT NOT NULL DEFAULT '',message TEXT NOT NULL DEFAULT '',created TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS event_page ON events(category,id DESC);
CREATE TABLE IF NOT EXISTS runs(id INTEGER PRIMARY KEY,source TEXT NOT NULL,targets TEXT NOT NULL,recursive INTEGER NOT NULL,status TEXT NOT NULL,created TEXT NOT NULL,finished TEXT NOT NULL DEFAULT '',discovered INTEGER NOT NULL DEFAULT 0,done INTEGER NOT NULL DEFAULT 0,skipped INTEGER NOT NULL DEFAULT 0,failed INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',request_key TEXT NOT NULL UNIQUE,plan INTEGER NOT NULL DEFAULT 0,config_summary TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS run_token_usage(run INTEGER PRIMARY KEY REFERENCES runs(id),token_count INTEGER NOT NULL DEFAULT 0,token_records INTEGER NOT NULL DEFAULT 0,token_estimated_records INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS run_items(id INTEGER PRIMARY KEY,run INTEGER NOT NULL REFERENCES runs(id),entry INTEGER NOT NULL REFERENCES entries(id),identity TEXT NOT NULL,size INTEGER NOT NULL,mtime TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'queued',message TEXT NOT NULL DEFAULT '',UNIQUE(run,identity));
CREATE INDEX IF NOT EXISTS item_queue ON run_items(run,status,id);
CREATE INDEX IF NOT EXISTS item_entry ON run_items(entry,status,id DESC);
CREATE TABLE IF NOT EXISTS schedules(id INTEGER PRIMARY KEY,name TEXT NOT NULL,targets TEXT NOT NULL,recursive INTEGER NOT NULL,kind TEXT NOT NULL,expression TEXT NOT NULL,timezone TEXT NOT NULL,enabled INTEGER NOT NULL,next TEXT NOT NULL,last TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS schedule_ticks(plan INTEGER NOT NULL,wall TEXT NOT NULL,created TEXT NOT NULL,PRIMARY KEY(plan,wall));
CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,csrf TEXT NOT NULL,expires INTEGER NOT NULL,fingerprint TEXT NOT NULL);
`
