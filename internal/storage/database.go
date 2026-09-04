// Package storage owns Foya's shared SQLite database.
package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const DatabaseName = "foya.db"

// Database is the process-owned SQLite connection shared by repositories.
type Database struct {
	*sql.DB
	path string
}

// Open creates or opens the database in dataDir and applies the current schema.
func Open(dataDir string) (*Database, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return OpenPath(filepath.Join(dataDir, DatabaseName))
}

// OpenPath opens a database at an explicit path. It is primarily useful for tests.
func OpenPath(path string) (*Database, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	for _, pragma := range []string{
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"journal_mode(WAL)",
		"synchronous(FULL)",
		"auto_vacuum(INCREMENTAL)",
	} {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()

	raw, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	if err := raw.Ping(); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db := &Database{DB: raw, path: path}
	if err := db.applySchema(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("chmod sqlite database: %w", err)
	}
	return db, nil
}

// Path returns the database file path.
func (db *Database) Path() string { return db.path }

func (db *Database) applySchema() error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("apply sqlite schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite schema: %w", err)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    parent_id TEXT NOT NULL DEFAULT '',
    spawned_by_json BLOB NOT NULL DEFAULT 'null',
    agent_ref TEXT NOT NULL DEFAULT '',
    agent_name TEXT NOT NULL DEFAULT '',
    agent_digest TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL,
    agent_mode TEXT NOT NULL,
    pre_plan_mode TEXT NOT NULL DEFAULT '',
    connection_id TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    reasoning_effort TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    approval_mode TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    title_is_manual INTEGER NOT NULL DEFAULT 0,
    pinned INTEGER NOT NULL DEFAULT 0,
    pinned_at_ns INTEGER,
    tasks_json BLOB NOT NULL DEFAULT '[]',
    created_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    agent_instructions TEXT NOT NULL DEFAULT '',
    allowed_tools_json BLOB NOT NULL DEFAULT '[]',
    agent_max_turns INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS sessions_parent_idx
    ON sessions(parent_id);
CREATE INDEX IF NOT EXISTS sessions_updated_idx
    ON sessions(updated_at_ns DESC);

CREATE TABLE IF NOT EXISTS events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    occurred_at_ns INTEGER NOT NULL,
    payload_json BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS events_session_seq_idx
    ON events(session_id, seq);
CREATE INDEX IF NOT EXISTS events_kind_time_idx
    ON events(kind, occurred_at_ns);
CREATE INDEX IF NOT EXISTS events_session_kind_time_idx
    ON events(session_id, kind, occurred_at_ns);

CREATE TABLE IF NOT EXISTS message_projection (
    event_seq INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    FOREIGN KEY(event_seq) REFERENCES events(seq) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS message_projection_active_idx
    ON message_projection(session_id, active, event_seq);

CREATE TABLE IF NOT EXISTS file_blobs (
    hash TEXT PRIMARY KEY,
    codec TEXT NOT NULL,
    raw_size INTEGER NOT NULL,
    data BLOB NOT NULL,
    created_at_ns INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS file_changes (
    event_seq INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    path TEXT NOT NULL,
    before_exists INTEGER NOT NULL,
    before_mode INTEGER NOT NULL,
    after_mode INTEGER NOT NULL,
    before_blob_hash TEXT,
    after_blob_hash TEXT NOT NULL,
    FOREIGN KEY(event_seq) REFERENCES events(seq) ON DELETE CASCADE,
    FOREIGN KEY(before_blob_hash) REFERENCES file_blobs(hash),
    FOREIGN KEY(after_blob_hash) REFERENCES file_blobs(hash)
);

CREATE INDEX IF NOT EXISTS file_changes_session_event_idx
    ON file_changes(session_id, event_seq);
CREATE INDEX IF NOT EXISTS file_changes_before_blob_idx
    ON file_changes(before_blob_hash);
CREATE INDEX IF NOT EXISTS file_changes_after_blob_idx
    ON file_changes(after_blob_hash);

CREATE TABLE IF NOT EXISTS file_rewind_journals (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    target_user_seq INTEGER NOT NULL,
    expected_head_seq INTEGER NOT NULL,
    state TEXT NOT NULL,
    created_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS file_rewind_journal_files (
    journal_id TEXT NOT NULL,
    path TEXT NOT NULL,
    before_exists INTEGER NOT NULL,
    before_mode INTEGER NOT NULL,
    before_blob_hash TEXT,
    after_exists INTEGER NOT NULL,
    after_mode INTEGER NOT NULL,
    after_blob_hash TEXT,
    PRIMARY KEY(journal_id, path),
    FOREIGN KEY(journal_id) REFERENCES file_rewind_journals(id) ON DELETE CASCADE,
    FOREIGN KEY(before_blob_hash) REFERENCES file_blobs(hash),
    FOREIGN KEY(after_blob_hash) REFERENCES file_blobs(hash)
);

CREATE TABLE IF NOT EXISTS usage_records (
    event_seq INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    total_tokens INTEGER NOT NULL,
    cached_tokens INTEGER NOT NULL,
    occurred_at_ns INTEGER NOT NULL,
    FOREIGN KEY(event_seq) REFERENCES events(seq) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS usage_records_time_idx
    ON usage_records(occurred_at_ns);
CREATE INDEX IF NOT EXISTS usage_records_model_time_idx
    ON usage_records(model, occurred_at_ns);

CREATE TABLE IF NOT EXISTS compaction_checkpoints (
    session_id TEXT PRIMARY KEY,
    event_seq INTEGER NOT NULL,
    checkpoint_json BLOB NOT NULL,
    FOREIGN KEY(event_seq) REFERENCES events(seq) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS stream_snapshots (
    session_id TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    payload_json BLOB NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    PRIMARY KEY(session_id, stream_id)
);

CREATE TABLE IF NOT EXISTS deleted_sessions (
    session_id TEXT PRIMARY KEY,
    deleted_at_ns INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cleanup_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    payload_json BLOB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at_ns INTEGER NOT NULL,
    updated_at_ns INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS usage_daily_ledger (
    date TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    request_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(date, model)
);

CREATE INDEX IF NOT EXISTS usage_daily_ledger_model_date_idx
    ON usage_daily_ledger(model, date);

CREATE TABLE IF NOT EXISTS usage_message_daily_ledger (
    date TEXT PRIMARY KEY,
    message_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS usage_session_days (
    date TEXT NOT NULL,
    root_session_id TEXT NOT NULL,
    PRIMARY KEY(date, root_session_id)
);

CREATE INDEX IF NOT EXISTS usage_session_days_session_idx
    ON usage_session_days(root_session_id, date);
`
