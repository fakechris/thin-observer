-- thin-observer SQLite schema.
-- This file is embedded via go:embed and executed on Store.Open() if tables
-- don't exist. For real migrations, add numbered migration files later.

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    root_path  TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS worktree (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES project(id),
    name            TEXT NOT NULL,
    path            TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'active', -- active | archived
    first_seen_at   TEXT NOT NULL,
    last_seen_at    TEXT NOT NULL,
    archived_at     TEXT
);

CREATE INDEX IF NOT EXISTS idx_worktree_project ON worktree(project_id);
CREATE INDEX IF NOT EXISTS idx_worktree_status  ON worktree(status);

CREATE TABLE IF NOT EXISTS snapshot (
    id            TEXT PRIMARY KEY,
    worktree_id   TEXT NOT NULL REFERENCES worktree(id),
    source_file   TEXT NOT NULL,
    timestamp     TEXT NOT NULL,
    raw_hash      TEXT NOT NULL,
    phases_json   TEXT NOT NULL,
    commit_sha    TEXT
);

CREATE INDEX IF NOT EXISTS idx_snapshot_worktree ON snapshot(worktree_id);
CREATE INDEX IF NOT EXISTS idx_snapshot_time     ON snapshot(worktree_id, timestamp);

CREATE TABLE IF NOT EXISTS task (
    id                TEXT PRIMARY KEY,
    worktree_id       TEXT NOT NULL REFERENCES worktree(id),
    project_id        TEXT NOT NULL REFERENCES project(id),
    current_title     TEXT NOT NULL,
    aliases_json      TEXT NOT NULL DEFAULT '[]',
    phase             TEXT,
    status            TEXT NOT NULL DEFAULT 'pending', -- pending | in_progress | done | skipped | lost | dropped
    confidence        REAL NOT NULL DEFAULT 1.0,
    source_file       TEXT,
    source_line       INTEGER,
    first_seen_at     TEXT NOT NULL,
    last_seen_at      TEXT NOT NULL,
    last_snapshot_id  TEXT REFERENCES snapshot(id),
    -- Inferred lineage
    renamed_from      TEXT REFERENCES task(id),
    split_from_json   TEXT NOT NULL DEFAULT '[]',
    merged_from_json  TEXT NOT NULL DEFAULT '[]',
    supersedes        TEXT REFERENCES task(id),
    -- Bookkeeping
    missing_in_rev    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_task_worktree ON task(worktree_id);
CREATE INDEX IF NOT EXISTS idx_task_project  ON task(project_id);
CREATE INDEX IF NOT EXISTS idx_task_status   ON task(status);

CREATE TABLE IF NOT EXISTS event (
    id         TEXT PRIMARY KEY,
    timestamp  TEXT NOT NULL,
    type       TEXT NOT NULL,
    task_id    TEXT,
    worktree_id TEXT,
    data_json  TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_event_time ON event(timestamp);
CREATE INDEX IF NOT EXISTS idx_event_task ON event(task_id);

CREATE TABLE IF NOT EXISTS override (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES task(id),
    kind        TEXT NOT NULL,      -- same_as | split_from | merged_from | drop | reopen | rename
    data_json   TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL,
    note        TEXT
);

CREATE INDEX IF NOT EXISTS idx_override_task ON override(task_id);
