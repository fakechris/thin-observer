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

-- plan_doc: one row per watched markdown file per worktree. Captures the
-- observer's latest belief about what the file is (task_plan / progress /
-- findings / detailed_plan / unknown) and when it was last seen.
CREATE TABLE IF NOT EXISTS plan_doc (
    id               TEXT PRIMARY KEY,
    worktree_id      TEXT NOT NULL REFERENCES worktree(id),
    source_file      TEXT NOT NULL,
    title            TEXT NOT NULL,
    kind             TEXT NOT NULL,
    last_snapshot_id TEXT REFERENCES snapshot(id),
    last_seen_at     TEXT NOT NULL,
    UNIQUE(worktree_id, source_file)
);

CREATE INDEX IF NOT EXISTS idx_plan_doc_worktree ON plan_doc(worktree_id);

-- plan_link: cross-document references extracted from markdown. to_source_file
-- is resolved to an absolute path at ingest time so the resolver can join
-- directly against plan_doc.source_file. to_plan_id is NULL until that target
-- plan_doc also exists in the same worktree.
CREATE TABLE IF NOT EXISTS plan_link (
    id             TEXT PRIMARY KEY,
    from_plan_id   TEXT NOT NULL REFERENCES plan_doc(id),
    to_source_file TEXT NOT NULL,
    to_plan_id     TEXT REFERENCES plan_doc(id),
    source_line    INTEGER NOT NULL DEFAULT 0,
    label          TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_plan_link_from ON plan_link(from_plan_id);
CREATE INDEX IF NOT EXISTS idx_plan_link_to   ON plan_link(to_plan_id);

-- task_revision: append-only per-snapshot task state. Exists so the time
-- machine UI can replay the board at any prior snapshot — the live `task`
-- row mutates in place and loses history otherwise. project_id is
-- denormalized on purpose so timelines can be scoped without a join.
CREATE TABLE IF NOT EXISTS task_revision (
    id           TEXT PRIMARY KEY,
    snapshot_id  TEXT NOT NULL REFERENCES snapshot(id),
    task_id      TEXT NOT NULL REFERENCES task(id),
    worktree_id  TEXT NOT NULL REFERENCES worktree(id),
    project_id   TEXT NOT NULL REFERENCES project(id),
    source_file  TEXT NOT NULL,
    title        TEXT NOT NULL,
    phase        TEXT,
    status       TEXT NOT NULL,
    confidence   REAL NOT NULL DEFAULT 1.0,
    source_line  INTEGER NOT NULL DEFAULT 0,
    recorded_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_task_revision_snapshot ON task_revision(snapshot_id);
CREATE INDEX IF NOT EXISTS idx_task_revision_task     ON task_revision(task_id);
CREATE INDEX IF NOT EXISTS idx_task_revision_worktree ON task_revision(worktree_id);
