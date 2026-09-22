package storage

// migrate applies upgrades atomically without rebuilding or dropping user data.
func (s *Store) migrate() error {
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version < 2 {
		_, err = tx.Exec(`
CREATE TABLE prompts(id INTEGER PRIMARY KEY,title TEXT NOT NULL,body TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,created TEXT NOT NULL,updated TEXT NOT NULL);
ALTER TABLE runs ADD COLUMN prompt_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE runs ADD COLUMN prompt_title TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN prompt_body TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN prompt_id INTEGER NOT NULL DEFAULT 1;
INSERT INTO schema_migrations VALUES(2,datetime('now'));
`)
		if err != nil {
			return err
		}
	}
	if version < 3 {
		_, err = tx.Exec(`
ALTER TABLE prompts ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX prompt_single_default ON prompts(is_default) WHERE is_default=1;
UPDATE prompts SET is_default=1 WHERE id=(SELECT min(id) FROM prompts);
DELETE FROM marks WHERE source='restore';
INSERT INTO schema_migrations VALUES(3,datetime('now'));
`)
		if err != nil {
			return err
		}
	}
	if version < 4 {
		_, err = tx.Exec(`
ALTER TABLE prompts RENAME TO model_configs;
ALTER TABLE model_configs ADD COLUMN settings TEXT NOT NULL DEFAULT '{}';
ALTER TABLE model_configs ADD COLUMN api_key TEXT NOT NULL DEFAULT '';
ALTER TABLE runs RENAME COLUMN prompt_id TO model_config_id;
ALTER TABLE runs RENAME COLUMN prompt_title TO model_config_title;
ALTER TABLE schedules RENAME COLUMN prompt_id TO model_config_id;
ALTER TABLE runs ADD COLUMN model_snapshot TEXT NOT NULL DEFAULT '';
INSERT INTO schema_migrations VALUES(4,datetime('now'));
`)
		if err != nil {
			return err
		}
	}
	if version < 5 {
		_, err = tx.Exec(`ALTER TABLE events ADD COLUMN raw_request TEXT NOT NULL DEFAULT ''; ALTER TABLE events ADD COLUMN raw_response TEXT NOT NULL DEFAULT ''; INSERT INTO schema_migrations VALUES(5,datetime('now'));`)
		if err != nil {
			return err
		}
	}
	if version < 6 {
		_, err = tx.Exec(`CREATE TABLE marks_next(identity TEXT NOT NULL, size INTEGER NOT NULL, mtime TEXT NOT NULL, source TEXT NOT NULL, operation INTEGER REFERENCES operations(id), PRIMARY KEY(identity,size,mtime));
INSERT INTO marks_next SELECT * FROM marks;
DROP TABLE marks;
ALTER TABLE marks_next RENAME TO marks;
INSERT INTO schema_migrations VALUES(6,datetime('now'));`)
		if err != nil {
			return err
		}
	}
	if version < 7 {
		_, err = tx.Exec(`ALTER TABLE runs ADD COLUMN started TEXT NOT NULL DEFAULT '';
CREATE TABLE notification_channels(kind TEXT PRIMARY KEY,enabled INTEGER NOT NULL DEFAULT 0,failure_only INTEGER NOT NULL DEFAULT 0,credentials TEXT NOT NULL,after_run INTEGER NOT NULL DEFAULT 0);
CREATE TABLE notification_deliveries(id INTEGER PRIMARY KEY,kind TEXT NOT NULL,run INTEGER NOT NULL,title TEXT NOT NULL,body TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',created TEXT NOT NULL,updated TEXT NOT NULL,next_attempt TEXT NOT NULL,UNIQUE(kind,run));
CREATE INDEX notification_pending ON notification_deliveries(status,next_attempt,id);
INSERT INTO schema_migrations VALUES(7,datetime('now'));`)
		if err != nil {
			return err
		}
	}
	if version < 8 {
		_, err = tx.Exec(`ALTER TABLE operations ADD COLUMN raw_request TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN raw_response TEXT NOT NULL DEFAULT '';
ALTER TABLE run_items ADD COLUMN original_path TEXT NOT NULL DEFAULT '';
INSERT INTO schema_migrations VALUES(8,datetime('now'));`)
		if err != nil {
			return err
		}
	}
	if version < 9 {
		if _, err = tx.Exec(`CREATE TABLE notification_preferences(id INTEGER PRIMARY KEY CHECK(id=1),timezone TEXT NOT NULL);
INSERT INTO notification_preferences VALUES(1,'UTC');
INSERT INTO schema_migrations VALUES(9,datetime('now'));`); err != nil {
			return err
		}
	}
	if version < 10 {
		if _, err = tx.Exec(`CREATE TABLE sampling_settings(id INTEGER PRIMARY KEY CHECK(id=1),settings TEXT NOT NULL);
ALTER TABLE runs ADD COLUMN sampling_snapshot TEXT NOT NULL DEFAULT '';
INSERT INTO schema_migrations VALUES(10,datetime('now'));`); err != nil {
			return err
		}
	}
	if version < 11 {
		if _, err = tx.Exec(`CREATE TABLE image_numberings(id TEXT PRIMARY KEY,parent INTEGER NOT NULL REFERENCES entries(id),status TEXT NOT NULL,created TEXT NOT NULL,fingerprint TEXT NOT NULL,undo_of TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '');
CREATE INDEX image_numbering_parent ON image_numberings(parent,created);
CREATE TABLE image_numbering_items(batch TEXT NOT NULL REFERENCES image_numberings(id),position INTEGER NOT NULL,entry INTEGER NOT NULL REFERENCES entries(id),version INTEGER NOT NULL,identity TEXT NOT NULL,size INTEGER NOT NULL,mtime TEXT NOT NULL,old_name TEXT NOT NULL,new_name TEXT NOT NULL,temp_name TEXT NOT NULL,current_name TEXT NOT NULL,pending_name TEXT NOT NULL DEFAULT '',operation INTEGER NOT NULL DEFAULT 0,undo_of INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(batch,entry));
INSERT INTO schema_migrations VALUES(11,datetime('now'));`); err != nil {
			return err
		}
	}
	if version < 12 {
		if _, err = tx.Exec(`CREATE TABLE system_settings(id INTEGER PRIMARY KEY CHECK(id=1),extract_workers INTEGER NOT NULL,cache_gb INTEGER NOT NULL);
INSERT INTO schema_migrations VALUES(12,datetime('now'));`); err != nil {
			return err
		}
	}
	if version < 13 {
		if _, err = tx.Exec(`ALTER TABLE system_settings ADD COLUMN stability_seconds INTEGER NOT NULL DEFAULT 30 CHECK(stability_seconds BETWEEN 0 AND 3600);
INSERT INTO schema_migrations VALUES(13,datetime('now'));`); err != nil {
			return err
		}
	}
	if version < 14 {
		if _, err = tx.Exec("ALTER TABLE events ADD COLUMN processing_log TEXT NOT NULL DEFAULT ''; INSERT INTO schema_migrations VALUES(14,datetime('now'));"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
