-- Current schema only. Keep the identity below in sync with schemaVersion.

CREATE TABLE library (
 id INTEGER PRIMARY KEY CHECK(id=1),
 root TEXT NOT NULL,
 identity TEXT NOT NULL
);

CREATE TABLE entries (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 parent INTEGER NOT NULL DEFAULT 0,
 name TEXT NOT NULL,
 kind TEXT NOT NULL,
 identity TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 present INTEGER NOT NULL DEFAULT 1,
 version INTEGER NOT NULL DEFAULT 1,
 observed TEXT NOT NULL
);

CREATE TABLE directory_state (
 dir INTEGER PRIMARY KEY REFERENCES entries(id),
 generation INTEGER NOT NULL DEFAULT 0,
 refreshed TEXT,
 error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE directory_items (
 dir INTEGER NOT NULL REFERENCES entries(id),
 generation INTEGER NOT NULL,
 entry INTEGER NOT NULL REFERENCES entries(id),
 name TEXT NOT NULL,
 kind TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 PRIMARY KEY(dir,generation,entry)
);

CREATE TABLE operations (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 entry INTEGER NOT NULL REFERENCES entries(id),
 parent INTEGER NOT NULL REFERENCES entries(id),
 old_name TEXT NOT NULL,
 new_name TEXT NOT NULL,
 identity TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 kind TEXT NOT NULL,
 source TEXT NOT NULL,
 status TEXT NOT NULL,
 undo_of INTEGER NOT NULL DEFAULT 0,
 undone INTEGER NOT NULL DEFAULT 0,
 request_key TEXT UNIQUE NOT NULL,
 created TEXT NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 run INTEGER NOT NULL DEFAULT 0,
 raw_request TEXT NOT NULL DEFAULT '',
 raw_response TEXT NOT NULL DEFAULT ''
);

CREATE TABLE events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 category TEXT NOT NULL,
 action TEXT NOT NULL,
 entry INTEGER NOT NULL DEFAULT 0,
 operation INTEGER NOT NULL DEFAULT 0,
 run INTEGER NOT NULL DEFAULT 0,
 old_path TEXT NOT NULL DEFAULT '',
 new_path TEXT NOT NULL DEFAULT '',
 message TEXT NOT NULL DEFAULT '',
 created TEXT NOT NULL,
 raw_request TEXT NOT NULL DEFAULT '',
 raw_response TEXT NOT NULL DEFAULT '',
 processing_log TEXT NOT NULL DEFAULT ''
);

CREATE TABLE runs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 source TEXT NOT NULL,
 targets TEXT NOT NULL,
 recursive INTEGER NOT NULL,
 status TEXT NOT NULL,
 created TEXT NOT NULL,
 finished TEXT NOT NULL DEFAULT '',
 discovered INTEGER NOT NULL DEFAULT 0,
 done INTEGER NOT NULL DEFAULT 0,
 skipped INTEGER NOT NULL DEFAULT 0,
 failed INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',
 request_key TEXT NOT NULL UNIQUE,
 plan INTEGER NOT NULL DEFAULT 0,
 config_summary TEXT NOT NULL DEFAULT '',
 model_config_id INTEGER NOT NULL DEFAULT 1,
 model_config_title TEXT NOT NULL DEFAULT '',
 prompt_body TEXT NOT NULL DEFAULT '',
 retry_snapshot TEXT NOT NULL DEFAULT '',
 model_snapshot TEXT NOT NULL DEFAULT '',
 started TEXT NOT NULL DEFAULT '',
 analysis_start_ms INTEGER GENERATED ALWAYS AS (video_rename_start_ms(started,created)) STORED,
 sampling_snapshot TEXT NOT NULL DEFAULT '',
 task TEXT NOT NULL DEFAULT 'rename'
);

CREATE TABLE run_token_usage (
 run INTEGER PRIMARY KEY REFERENCES runs(id),
 token_count INTEGER NOT NULL DEFAULT 0,
 token_records INTEGER NOT NULL DEFAULT 0,
 token_estimated_records INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE run_items (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 run INTEGER NOT NULL REFERENCES runs(id),
 entry INTEGER NOT NULL REFERENCES entries(id),
 identity TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued',
 message TEXT NOT NULL DEFAULT '',
 original_path TEXT NOT NULL DEFAULT '',
 started_ms INTEGER NOT NULL DEFAULT 0,
 finished_ms INTEGER NOT NULL DEFAULT 0,
 UNIQUE(run,identity)
);

CREATE TABLE schedules (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 name TEXT NOT NULL,
 targets TEXT NOT NULL,
 recursive INTEGER NOT NULL,
 kind TEXT NOT NULL,
 expression TEXT NOT NULL,
 timezone TEXT NOT NULL,
 enabled INTEGER NOT NULL,
 next TEXT NOT NULL,
 last TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL DEFAULT 1,
 model_config_id INTEGER NOT NULL DEFAULT 1,
 task TEXT NOT NULL DEFAULT 'rename'
);

CREATE TABLE schedule_ticks (
 plan INTEGER NOT NULL,
 wall TEXT NOT NULL,
 created TEXT NOT NULL,
 PRIMARY KEY(plan,wall)
);

CREATE TABLE sessions (
 token TEXT PRIMARY KEY,
 expires INTEGER NOT NULL,
 fingerprint TEXT NOT NULL
);

CREATE TABLE prompts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 title TEXT NOT NULL,
 body TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 created TEXT NOT NULL,
 updated TEXT NOT NULL
);

CREATE TABLE model_configs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 title TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 created TEXT NOT NULL,
 updated TEXT NOT NULL,
 is_default INTEGER NOT NULL DEFAULT 0,
 settings TEXT NOT NULL DEFAULT '{}',
 api_key TEXT NOT NULL DEFAULT '',
 prompt_id INTEGER NOT NULL REFERENCES prompts(id)
);

CREATE TABLE marks (
 identity TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 source TEXT NOT NULL,
 operation INTEGER REFERENCES operations(id),
 PRIMARY KEY(identity,size,mtime_ns)
);

CREATE TABLE notification_channels (
 kind TEXT PRIMARY KEY,
 enabled INTEGER NOT NULL DEFAULT 0,
 failure_only INTEGER NOT NULL DEFAULT 0,
 credentials TEXT NOT NULL,
 after_run INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE notification_deliveries (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL,
 run INTEGER NOT NULL,
 title TEXT NOT NULL,
 body TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending',
 attempts INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',
 created TEXT NOT NULL,
 updated TEXT NOT NULL,
 next_attempt TEXT NOT NULL,
 UNIQUE(kind,run)
);

CREATE TABLE notification_preferences (
 id INTEGER PRIMARY KEY CHECK(id=1),
 timezone TEXT NOT NULL
);

CREATE TABLE sampling_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 settings TEXT NOT NULL
);

CREATE TABLE image_numberings (
 id TEXT PRIMARY KEY,
 parent INTEGER NOT NULL REFERENCES entries(id),
 status TEXT NOT NULL,
 created TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 undo_of TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 media TEXT NOT NULL DEFAULT 'image'
);

CREATE TABLE image_numbering_items (
 batch TEXT NOT NULL REFERENCES image_numberings(id),
 position INTEGER NOT NULL,
 entry INTEGER NOT NULL REFERENCES entries(id),
 version INTEGER NOT NULL,
 identity TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 old_name TEXT NOT NULL,
 new_name TEXT NOT NULL,
 temp_name TEXT NOT NULL,
 current_name TEXT NOT NULL,
 pending_name TEXT NOT NULL DEFAULT '',
 operation INTEGER NOT NULL DEFAULT 0,
 undo_of INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(batch,entry)
);

CREATE TABLE system_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 extract_workers INTEGER NOT NULL,
 cache_gb INTEGER NOT NULL,
 stability_seconds INTEGER NOT NULL DEFAULT 30 CHECK(stability_seconds BETWEEN 0 AND 3600),
 numbering TEXT NOT NULL DEFAULT '',
 fixed_prompt TEXT NOT NULL DEFAULT '',
 policy TEXT NOT NULL DEFAULT ''
);

CREATE TABLE token_usage_records (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 run INTEGER NOT NULL REFERENCES runs(id),
 item INTEGER NOT NULL REFERENCES run_items(id),
 attempt INTEGER NOT NULL DEFAULT 1,
 model_config_id INTEGER,
 model_config_title TEXT,
 config_summary TEXT,
 recorded_at INTEGER NOT NULL,
 tokens INTEGER NOT NULL,
 records INTEGER NOT NULL DEFAULT 1,
 estimated_records INTEGER NOT NULL DEFAULT 0,
 UNIQUE(item,attempt)
);

CREATE TABLE model_attempts (
 item INTEGER NOT NULL REFERENCES run_items(id),
 attempt INTEGER NOT NULL,
 model_config_id INTEGER NOT NULL,
 model_config_title TEXT NOT NULL,
 model TEXT NOT NULL,
 fallback INTEGER NOT NULL,
 started TEXT NOT NULL,
 duration_ms INTEGER NOT NULL,
 status TEXT NOT NULL,
 message TEXT NOT NULL,
 PRIMARY KEY(item,attempt)
);

CREATE TABLE extraction_records (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 run INTEGER NOT NULL DEFAULT 0,
 entry INTEGER NOT NULL DEFAULT 0,
 path TEXT NOT NULL,
 source TEXT NOT NULL,
 status TEXT NOT NULL,
 started TEXT NOT NULL,
 finished TEXT NOT NULL DEFAULT '',
 duration_ms INTEGER NOT NULL DEFAULT 0,
 cached INTEGER NOT NULL DEFAULT 0,
 frames INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT '',
 processing_log TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX entry_path ON entries(parent,name) WHERE present=1;

CREATE INDEX entry_identity ON entries(identity);

CREATE INDEX entry_parent_id ON entries(parent,id) WHERE present=1;

CREATE INDEX directory_entry ON directory_items(dir,entry);

CREATE INDEX directory_entry_fk ON directory_items(entry);

CREATE INDEX directory_natural ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name COLLATE NATURAL_NAME,entry);

CREATE INDEX directory_natural_desc ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name COLLATE NATURAL_NAME DESC,entry DESC);

CREATE INDEX directory_sort ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name,entry);

CREATE INDEX directory_sort_desc ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name DESC,entry DESC);

CREATE INDEX operation_entry ON operations(entry,id DESC);

CREATE INDEX operation_parent ON operations(parent);

CREATE INDEX operation_run ON operations(run,id DESC);

CREATE INDEX event_page ON events(category,id DESC);

CREATE INDEX event_run ON events(category,run,id DESC);

CREATE INDEX event_automatic_failure_entry ON events(entry)
 WHERE category='failure' AND action='automatic';

CREATE INDEX run_status ON runs(status,id);

CREATE INDEX run_active ON runs(id) WHERE status IN ('queued','scanning','running','paused','cancelling');

CREATE INDEX run_profile ON runs(model_config_id,id DESC);

CREATE INDEX item_queue ON run_items(run,status,id);

CREATE INDEX item_page ON run_items(run,id);

CREATE INDEX item_logs ON run_items(run,MAX(started_ms,finished_ms) DESC,id DESC) WHERE status<>'queued';

CREATE INDEX item_failures ON run_items(run,MAX(started_ms,finished_ms) DESC,id DESC) WHERE status='failed';

CREATE INDEX item_entry ON run_items(entry,status,id DESC);

CREATE UNIQUE INDEX model_single_default ON model_configs(is_default) WHERE is_default=1;

CREATE INDEX notification_pending ON notification_deliveries(status,next_attempt,id);

CREATE INDEX image_numbering_parent ON image_numberings(parent,created);

CREATE INDEX image_numbering_entry ON image_numbering_items(entry);

CREATE INDEX token_usage_time ON token_usage_records(recorded_at);

CREATE INDEX token_usage_run ON token_usage_records(run,recorded_at);

CREATE INDEX extraction_page ON extraction_records(status,id DESC);

CREATE INDEX extraction_run ON extraction_records(run,id DESC);

INSERT INTO notification_preferences VALUES(1,'UTC');

PRAGMA user_version=20261028;

CREATE INDEX run_analysis_start ON runs(analysis_start_ms,id);

-- Transactional revisions invalidate immutable, bounded report caches.
CREATE TABLE report_revision (name TEXT PRIMARY KEY, revision INTEGER NOT NULL DEFAULT 0);
INSERT INTO report_revision(name) VALUES('runs'),('usage'),('run_options'),('usage_options');
CREATE TRIGGER revision_runs_insert AFTER INSERT ON runs BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('runs','usage','run_options','usage_options');
END;

CREATE TRIGGER revision_runs_update AFTER UPDATE ON runs BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('runs','usage','run_options','usage_options');
END;

CREATE TRIGGER revision_runs_delete AFTER DELETE ON runs BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('runs','usage','run_options','usage_options');
END;

CREATE TRIGGER revision_run_items_insert AFTER INSERT ON run_items BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('runs');
END;

CREATE TRIGGER revision_run_items_update AFTER UPDATE ON run_items BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('runs');
END;

CREATE TRIGGER revision_run_items_delete AFTER DELETE ON run_items BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('runs');
END;

CREATE TRIGGER revision_token_usage_records_insert AFTER INSERT ON token_usage_records BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage','usage_options');
END;

CREATE TRIGGER revision_token_usage_records_update AFTER UPDATE ON token_usage_records BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage','usage_options');
END;

CREATE TRIGGER revision_token_usage_records_delete AFTER DELETE ON token_usage_records BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage','usage_options');
END;

CREATE TRIGGER revision_run_token_usage_insert AFTER INSERT ON run_token_usage BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage');
END;

CREATE TRIGGER revision_run_token_usage_update AFTER UPDATE ON run_token_usage BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage');
END;

CREATE TRIGGER revision_run_token_usage_delete AFTER DELETE ON run_token_usage BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage');
END;

CREATE TRIGGER revision_model_configs_insert AFTER INSERT ON model_configs BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage','usage_options');
END;

CREATE TRIGGER revision_model_configs_update AFTER UPDATE ON model_configs BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage','usage_options');
END;

CREATE TRIGGER revision_model_configs_delete AFTER DELETE ON model_configs BEGIN
 UPDATE report_revision SET revision=revision+1 WHERE name IN ('usage','usage_options');
END;

CREATE INDEX entry_directories ON entries(parent,id) WHERE present=1 AND kind='directory';
CREATE TABLE directory_revision (dir INTEGER PRIMARY KEY, revision INTEGER NOT NULL DEFAULT 0);
CREATE TRIGGER directory_entries_insert AFTER INSERT ON entries WHEN NEW.present=1 BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT NEW.parent,1 WHERE NEW.parent>0 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_entries_delete AFTER DELETE ON entries WHEN OLD.present=1 BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT OLD.parent,1 WHERE OLD.parent>0 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_entries_update AFTER UPDATE OF parent,name,kind,identity,size,mtime_ns,present ON entries WHEN (OLD.present=1 OR NEW.present=1) AND (OLD.parent IS NOT NEW.parent OR OLD.name IS NOT NEW.name OR OLD.kind IS NOT NEW.kind OR OLD.identity IS NOT NEW.identity OR OLD.size IS NOT NEW.size OR OLD.mtime_ns IS NOT NEW.mtime_ns OR OLD.present IS NOT NEW.present) BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT OLD.parent,1 WHERE OLD.parent>0 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
 INSERT INTO directory_revision(dir,revision) SELECT NEW.parent,1 WHERE NEW.parent>0 AND NEW.parent<>OLD.parent ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_marks_insert AFTER INSERT ON marks BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT DISTINCT parent,1 FROM entries WHERE identity=NEW.identity AND size=NEW.size AND mtime_ns=NEW.mtime_ns AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_run_items_insert AFTER INSERT ON run_items WHEN NEW.status IN ('queued','running') BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT parent,1 FROM entries WHERE id=NEW.entry AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_directory_items_insert AFTER INSERT ON directory_items BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT NEW.dir,1 FROM directory_state WHERE dir=NEW.dir AND generation=NEW.generation ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_marks_delete AFTER DELETE ON marks BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT DISTINCT parent,1 FROM entries WHERE identity=OLD.identity AND size=OLD.size AND mtime_ns=OLD.mtime_ns AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_run_items_delete AFTER DELETE ON run_items WHEN OLD.status IN ('queued','running') BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT parent,1 FROM entries WHERE id=OLD.entry AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_directory_items_delete AFTER DELETE ON directory_items BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT OLD.dir,1 FROM directory_state WHERE dir=OLD.dir AND generation=OLD.generation ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_marks_update AFTER UPDATE ON marks BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT DISTINCT parent,1 FROM entries WHERE identity=OLD.identity AND size=OLD.size AND mtime_ns=OLD.mtime_ns AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
 INSERT INTO directory_revision(dir,revision) SELECT DISTINCT parent,1 FROM entries WHERE identity=NEW.identity AND size=NEW.size AND mtime_ns=NEW.mtime_ns AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_run_items_update AFTER UPDATE ON run_items WHEN (OLD.status IN ('queued','running') OR NEW.status IN ('queued','running')) AND (OLD.entry IS NOT NEW.entry OR OLD.run IS NOT NEW.run OR OLD.status IS NOT NEW.status) BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT parent,1 FROM entries WHERE id=OLD.entry AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
 INSERT INTO directory_revision(dir,revision) SELECT parent,1 FROM entries WHERE id=NEW.entry AND present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_directory_items_update AFTER UPDATE ON directory_items BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT OLD.dir,1 FROM directory_state WHERE dir=OLD.dir AND generation=OLD.generation ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
 INSERT INTO directory_revision(dir,revision) SELECT NEW.dir,1 FROM directory_state WHERE dir=NEW.dir AND generation=NEW.generation ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_runs_update AFTER UPDATE OF status ON runs WHEN OLD.status IS NOT NEW.status BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT DISTINCT e.parent,1 FROM run_items i JOIN entries e ON e.id=i.entry WHERE i.run=NEW.id AND i.status IN ('queued','running') AND e.present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE INDEX operation_pending ON operations(entry) WHERE status IN ('prepared','review');
CREATE INDEX numbering_pending ON image_numberings(parent) WHERE status IN ('running','rollback','review');
CREATE INDEX entry_scan_orphan ON entries(parent,id) WHERE present=0 AND observed GLOB 'directory-scan:*';
CREATE TRIGGER directory_runs_insert AFTER INSERT ON runs BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT DISTINCT e.parent,1 FROM run_items i JOIN entries e ON e.id=i.entry WHERE i.run=NEW.id AND i.status IN ('queued','running') AND e.present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

CREATE TRIGGER directory_runs_delete BEFORE DELETE ON runs BEGIN
 INSERT INTO directory_revision(dir,revision) SELECT DISTINCT e.parent,1 FROM run_items i JOIN entries e ON e.id=i.entry WHERE i.run=OLD.id AND i.status IN ('queued','running') AND e.present=1 ON CONFLICT(dir) DO UPDATE SET revision=revision+1;
END;

-- First HTTP result per idempotency key. Pending records are retained after a
-- crash so an uncertain file mutation cannot be repeated automatically.
CREATE TABLE request_results (
 key TEXT PRIMARY KEY,
 fingerprint TEXT NOT NULL,
 status INTEGER NOT NULL,
 headers TEXT NOT NULL,
 body BLOB NOT NULL,
 expires_ms INTEGER NOT NULL
);
CREATE INDEX request_results_expiry ON request_results(expires_ms) WHERE status<>0;

CREATE TABLE authentication (id INTEGER PRIMARY KEY CHECK(id=1), salt TEXT NOT NULL);
