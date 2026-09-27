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
 mtime TEXT NOT NULL,
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
 mtime TEXT NOT NULL,
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
 mtime TEXT NOT NULL,
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
 mtime TEXT NOT NULL,
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
 csrf TEXT NOT NULL,
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
 mtime TEXT NOT NULL,
 source TEXT NOT NULL,
 operation INTEGER REFERENCES operations(id),
 PRIMARY KEY(identity,size,mtime)
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
 mtime TEXT NOT NULL,
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

CREATE INDEX directory_natural ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name COLLATE NATURAL_NAME,entry);

CREATE INDEX directory_natural_desc ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name COLLATE NATURAL_NAME DESC,entry DESC);

CREATE INDEX directory_sort ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name,entry);

CREATE INDEX directory_sort_desc ON directory_items(dir,generation,CASE WHEN kind='directory' THEN 0 ELSE 1 END,name DESC,entry DESC);

CREATE INDEX operation_entry ON operations(entry,id DESC);

CREATE INDEX event_page ON events(category,id DESC);

CREATE INDEX event_automatic_failure_entry ON events(entry)
 WHERE category='failure' AND action='automatic';

CREATE INDEX item_queue ON run_items(run,status,id);

CREATE INDEX item_entry ON run_items(entry,status,id DESC);

CREATE UNIQUE INDEX model_single_default ON model_configs(is_default) WHERE is_default=1;

CREATE INDEX notification_pending ON notification_deliveries(status,next_attempt,id);

CREATE INDEX image_numbering_parent ON image_numberings(parent,created);

CREATE INDEX token_usage_time ON token_usage_records(recorded_at);

CREATE INDEX token_usage_run ON token_usage_records(run,recorded_at);

CREATE INDEX extraction_page ON extraction_records(status,id DESC);

CREATE INDEX extraction_run ON extraction_records(run,id DESC);

INSERT INTO notification_preferences VALUES(1,'UTC');

PRAGMA user_version=20260929;
