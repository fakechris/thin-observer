# Plan View And Time Machine Development Brief

> **For Claude:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task.

**Goal:** Build a faithful, multi-plan observer experience for `thin-observer`: discover standalone plan files, show project/worktree/plan hierarchy, expose raw source facts, and add a time machine view over historical task state.

**Architecture:** Keep `thin-observer` passive. The watcher discovers markdown files, the parser extracts observations, ingest stores append-friendly snapshots/events plus current task state, and the web UI presents both interpreted boards and raw facts. Do not write to watched markdown files.

**Tech Stack:** Go 1.25, `fsnotify`, SQLite via `modernc.org/sqlite`, `html/template`, embedded CSS/templates, Cobra CLI.

## Project Background

`thin-observer` is a passive observer for coding-agent plan files. Its purpose is to turn local markdown planning artifacts into a kanban-like view without requiring Claude, Codex, Cursor, or any agent to follow a protocol.

The important invariant is that the agent-owned markdown remains the source of truth. `thin-observer` reads those files, parses best-effort structure, stores observer-side state in SQLite, and renders views. It must not mutate `task_plan.md`, `todo.md`, `progress.md`, `findings.md`, or other watched plan files.

The product is now being used across many registered projects and git worktrees. A flat project-wide task board is no longer enough because large projects often have:

- a root `task_plan.md` acting as an index or current summary;
- a `progress.md` recording sessions and completed phases;
- standalone detailed plans under `docs/plans/*.md`;
- multiple worktrees with their own local plan files;
- tasks whose useful context lives in the original markdown lines, not only in the extracted title.

## User Scenarios

### Scenario 1: Project Board With Many Worktrees

The user opens:

```text
http://127.0.0.1:7787/?project=01KPQ6F149J1K8B2KRYWBPZBR4
```

This project is `openclaw-template`. The board currently mixes tasks from the main worktree and several auxiliary worktrees. Without a plan-level view, the user cannot quickly tell which cards came from the roadmap summary, a detailed phase plan, or an older worktree.

### Scenario 2: Standalone Plan Files

Recent work in `openclaw-template` changed multiple files:

```text
task_plan.md:1
  Replaced older external discovery / Phase 24 state with current roadmap,
  Phase 27 TODO, decisions, and non-goals.

progress.md:5
  Marked Phase 26 merged, Milestone 7 complete, and added Phase 27 planning.

docs/plans/2026-04-14-local-knowledge-workbench-milestone.md:319
  Marked Milestone 7 complete and Milestone 9A active next.

docs/plans/2026-04-21-phase27-background-intelligence-orchestration-closeout.md:1
  Defined the next stage as orchestration closeout for action queue, worker,
  handler registry, resolver, and run ledger.
```

Current behavior is incomplete: root plan files are ingested, but `docs/plans/*.md` discovery/watch support is missing or unreliable. Also, when `task_plan.md` references a detailed plan file, `thin-observer` does not currently model that link.

### Scenario 3: Task Detail Needs Source Context

Example task:

```text
http://127.0.0.1:7787/task/01KPQBDE1J9BG8G1TJEJH41PDE
```

The task title is:

```text
Phase 27 Task 2: Worker runtime visibility
```

The task source is:

```text
/Users/chris/Documents/openclaw-template/task_plan.md:68
```

The structured task row only contains title/status/phase/source/lineage/event metadata. The useful details are in the original markdown around line 68:

```markdown
2. **Phase 27 Task 2: Worker runtime visibility**
   - Make action worker state visible beside pipeline runtime state.
   - Expose worker mode, current action, duration, and safe-only state.
   - Ensure `/api/runtime`, `/`, and `/actions` tell the same story.
```

The UI now needs a faithful source context mode for this kind of task.

### Scenario 4: Time Machine

The user needs a raw, faithful mode that can answer:

- What did this worktree look like at time T?
- Which markdown files were observed?
- Which snapshot was created?
- What git commit was the worktree at?
- What complete task state did `thin-observer` believe at that point?
- What source lines and parsed JSON produced that state?

This should be a fact view, not a polished inference view.

## Current Raw Data And Constraints

### Config

Project registrations live at:

```text
/Users/chris/.config/thin-observer/config.yaml
```

Example relevant project:

```yaml
- name: openclaw-template
  path: /Users/chris/Documents/openclaw-template
```

### State DB

SQLite path:

```text
~/.local/state/thin-observer/db.sqlite
```

Important current tables:

- `project`
- `worktree`
- `snapshot`
- `task`
- `event`
- `override`

The existing schema has `snapshot.commit_sha`, but the ingest path does not reliably populate it yet.

### Schema Migration Policy

`internal/store/schema.sql` currently uses idempotent `CREATE TABLE IF NOT EXISTS` statements. For this brief:

- Adding new tables and indexes through idempotent schema additions is acceptable.
- Adding columns to existing tables is not acceptable without first introducing a numbered migration mechanism.
- Do not sneak `ALTER TABLE` calls into `Store.Open`.
- If a future phase needs a new column on an existing table, pause and add explicit migrations before implementing that phase.

### Existing Snapshot Model

Current `snapshot` rows store:

- `worktree_id`
- `source_file`
- `timestamp`
- `raw_hash`
- `phases_json`
- `commit_sha`

Current `task` rows are upserted current state. They are not a complete historical record because subsequent ingests mutate the row.

### Historical Retention Policy

`task_revision` is intentionally append-only for the first implementation. Do not add automatic pruning or TTL. A time machine that silently deletes history would violate the product's "faithful observer" premise.

Expected growth is acceptable for local SQLite in the MVP. If storage becomes an issue later, add an explicit user-run maintenance command that reports what would be removed before deleting anything. Do not prune automatically during ingest.

### Current Parser Behavior And Upgrade Risk

The parser recognizes checkbox tasks:

```markdown
- [ ] pending task
- [x] done task
- [/] in-progress task
- [~] skipped task
```

It also now needs to treat top-level plain list items as tasks only inside task-carrying sections such as:

- `TODO`
- `Tasks`
- `Next Steps`
- `Action Items`

Sub-bullets are task detail context and must not become separate tasks.

This is a material semantic change. It can reshuffle existing boards on upgrade because previously invisible plain list items may become tasks. Implement it as a dedicated phase with fixtures and mention it in release notes or PR description.

### Important Safety Rule

Do not write to watched markdown files. This includes:

- `task_plan.md`
- `todo.md`
- `tasks.md`
- `progress.md`
- `findings.md`
- files under future watched plan directories such as `docs/plans/*.md`

Development notes and handoff docs should live outside watched plan locations, for example under `docs/dev-briefs/`.

## Product Goals

1. **Discover all relevant plan files.**
   `docs/plans/*.md` must be discovered during `watch --once` and watched during long-running `watch`.

2. **Show plan hierarchy.**
   A project should not render as one undifferentiated pile of cards. The user should be able to view tasks by project, worktree, and plan file.

3. **Model plan file links.**
   If `task_plan.md` references `docs/plans/foo.md`, preserve that relationship as observer state.

4. **Expose faithful source context.**
   Task pages should link to original markdown context around the source line.

5. **Expose raw facts.**
   Provide a mode that displays snapshots, source files, parsed JSON, events, task rows, and commit SHA without hiding uncertainty behind a kanban interpretation.

6. **Support time machine.**
   Users should be able to pick a worktree and a timestamp/snapshot and see the complete task state as it was observed then.

## Non-Goals

- Do not require agents to follow a new protocol.
- Do not modify plan markdown files.
- Do not infer perfect semantics from arbitrary markdown.
- Do not auto-merge tasks across files unless there is explicit lineage or a user override.
- Do not replace the current kanban; add hierarchy and raw modes around it.

## Implementation Plan

### Phase 1: Fix `docs/plans/*.md` Discovery And Watch

**Files:**

- Modify: `internal/watcher/watcher.go`
- Test: `internal/watcher/watcher_test.go`
- Possibly modify: `cmd/thin-observer/main.go` only if logging needs to expose discovered file counts.

**Behavior:**

- Initial ingest should scan:
  - worktree root plan files;
  - `plans/*.md`;
  - `docs/plans/*.md`;
  - `.workgraph/plans/*.md`.
- Runtime watch should register:
  - worktree root;
  - `plans`;
  - `docs/plans`;
  - `.workgraph/plans`.
- Do not recursively watch all of `docs`.

**Tests:**

1. Write failing test for `ListKnownPlanFiles` that creates:

   ```text
   task_plan.md
   docs/plans/phase27.md
   docs/random.md
   ```

   Expected:

   - includes `task_plan.md`;
   - includes `docs/plans/phase27.md`;
   - excludes `docs/random.md`.

2. Write failing test for `AddWorktree` if testable with temp dirs and watcher internals, or factor directory discovery into a pure helper and test that helper.

3. Run:

   ```bash
   go test ./internal/watcher -count=1
   ```

### Phase 2: Make Plain Task-Section Lists Explicit Parser Semantics

**Files:**

- Modify: `internal/parser/types.go` only if new fields are needed.
- Modify: `internal/parser/parser.go`
- Test: `internal/parser/parser_test.go`

**Behavior:**

- Checkbox list items remain tasks everywhere:

  ```markdown
  - [ ] pending task
  - [x] done task
  ```

- Top-level plain list items become tasks only under task-carrying headings:

  ```markdown
  ## Current TODO

  1. **Docs closeout**
     - Mark Phase 26 complete everywhere.
  2. **Phase 27 Task 2: Worker runtime visibility**
     - Make action worker state visible beside pipeline runtime state.
  ```

- Task-carrying headings include:
  - `TODO`
  - `Tasks`
  - `Next Steps`
  - `Action Items`

- Indented sub-bullets are detail context and must not become separate tasks.
- Non-task sections such as `Decisions`, `Progress Log`, `Actions taken`, and `Files modified` must not produce tasks from plain list items.

**Tests:**

1. Fixture with `## Current TODO` and numbered bold items should produce tasks.
2. Fixture with nested bullets under a numbered item should not produce extra tasks.
3. Fixture with `## Decisions` and numbered list should produce zero tasks.
4. Existing checkbox fixtures must still pass unchanged.

**Upgrade note:**

Mention in the PR description that this parser change can expose tasks that were previously invisible. This is expected, but it is a user-visible semantic change.

### Phase 3: Extract Plan Links From Markdown

**Files:**

- Modify: `internal/parser/types.go`
- Modify: `internal/parser/parser.go`
- Test: `internal/parser/parser_test.go`

**Parser output:**

Add `Links []PlanLink` to `PlanDoc`.

Suggested type:

```go
type PlanLink struct {
    Target string `json:"target"`
    Label  string `json:"label,omitempty"`
    Line   int    `json:"line"`
}
```

**Recognize:**

- markdown links:

  ```markdown
  [Phase 27 plan](docs/plans/2026-04-21-phase27.md)
  ```

- bare markdown paths:

  ```text
  docs/plans/2026-04-21-phase27.md
  ```

Only keep local `.md` targets. Ignore HTTP URLs.

**Tests:**

- Markdown link is extracted with label and line.
- Bare `docs/plans/*.md` path is extracted.
- `https://example.com/foo.md` is ignored.

### Phase 4: Add `plan_doc` And `plan_link`

**Files:**

- Modify: `internal/store/schema.sql`
- Modify: `internal/store/store.go`
- Modify: `internal/store/types.go`
- Test: `internal/store/store_test.go`
- Modify: `internal/ingest/ingest.go`
- Test: `internal/ingest/ingest_test.go`

**Schema:**

Add `plan_doc`:

```sql
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
```

Add `plan_link`:

```sql
CREATE TABLE IF NOT EXISTS plan_link (
    id             TEXT PRIMARY KEY,
    from_plan_id   TEXT NOT NULL REFERENCES plan_doc(id),
    to_source_file TEXT NOT NULL,
    to_plan_id     TEXT REFERENCES plan_doc(id),
    source_line    INTEGER NOT NULL DEFAULT 0,
    label          TEXT NOT NULL DEFAULT ''
);
```

**Plan kind rules:**

- root `task_plan.md`, `plan.md`, `todo.md`, `tasks.md` -> `task_plan`
- `progress.md`, `session.md`, `log.md` -> `progress`
- `findings.md`, `research.md`, `notes.md` -> `findings`
- path contains `/docs/plans/` or `/plans/` -> `detailed_plan`
- fallback -> `unknown`

**Plan title rules:**

- frontmatter `title`, if present;
- otherwise first H1/H2 text;
- otherwise basename.

**Tests:**

- Upsert and query plan docs by worktree/source file.
- Ingest creates/updates a `plan_doc` row.
- Task counts are derived in queries with `COUNT(*)` over `task.source_file`; do not store `plan_doc.task_count`.
- Ingest stores links extracted from parser `PlanDoc.Links`.

### Phase 5: Populate `snapshot.commit_sha`

**Files:**

- Create: `internal/gitutil/git.go`
- Test: `internal/gitutil/git_test.go`
- Modify: `internal/ingest/ingest.go`
- Test: `internal/ingest/ingest_test.go`

**Behavior:**

At the snapshot boundary inside ingest, record the current worktree commit:

```bash
git -C <worktree path> rev-parse HEAD
```

Store it in `snapshot.commit_sha`.

**Rules:**

- `cmd/thin-observer/main.go` must not know about git.
- `internal/ingest` is the snapshot producer, so commit capture belongs there.
- `internal/gitutil` should contain the small git helper.
- If git command fails, leave commit SHA empty and log a warning.
- Do not block ingest.
- Do not run network commands.

**Tests:**

- Helper returns a 40-character SHA in a temp git repo.
- Helper returns empty string/error for non-git directory.
- Ingest stores commit SHA on inserted snapshots when the worktree is a git repo.

### Phase 6: Add `task_revision`

**Files:**

- Modify: `internal/store/schema.sql`
- Modify: `internal/store/types.go`
- Modify: `internal/store/store.go`
- Modify: `internal/ingest/ingest.go`
- Test: `internal/ingest/ingest_test.go`

**Schema:**

```sql
CREATE TABLE IF NOT EXISTS task_revision (
    id               TEXT PRIMARY KEY,
    snapshot_id      TEXT NOT NULL REFERENCES snapshot(id),
    task_id          TEXT NOT NULL REFERENCES task(id),
    worktree_id      TEXT NOT NULL REFERENCES worktree(id),
    project_id       TEXT NOT NULL REFERENCES project(id),
    title            TEXT NOT NULL,
    aliases_json     TEXT NOT NULL DEFAULT '[]',
    phase            TEXT,
    status           TEXT NOT NULL,
    confidence       REAL NOT NULL DEFAULT 1.0,
    source_file      TEXT,
    source_line      INTEGER,
    last_seen_at     TEXT NOT NULL,
    renamed_from     TEXT,
    split_from_json  TEXT NOT NULL DEFAULT '[]',
    merged_from_json TEXT NOT NULL DEFAULT '[]',
    supersedes       TEXT,
    missing_in_rev   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_task_revision_snapshot ON task_revision(snapshot_id);
CREATE INDEX IF NOT EXISTS idx_task_revision_worktree ON task_revision(worktree_id, snapshot_id);
```

`project_id` is denormalized deliberately for query ergonomics in project-level timelines. It is derivable through `worktree_id`, so tests must ensure it matches the worktree's project when revisions are written.

**Behavior:**

After each successful snapshot apply, write task revisions representing the complete task state for the relevant worktree after that apply.

This is what powers time machine. Do not rely on current `task` rows to reconstruct history.

Do not add TTL or automatic pruning. Revisions are local append-only facts. If storage management is needed later, implement an explicit user-run maintenance command with a dry-run mode.

**Tests:**

- First ingest writes revisions for created tasks.
- Second ingest writes a new revision set for updated state.
- Query by snapshot returns historical status even after current task row changes later.
- Project ID on each revision matches the task/worktree project.

### Phase 7: Plan View UI

**Files:**

- Modify: `internal/web/server.go`
- Add/modify templates under `internal/web/templates/`
- Modify: `internal/web/static/style.css`
- Test: `internal/web/server_test.go`

**Routes:**

- Existing project board remains:

  ```text
  /?project=<project_id>
  ```

- Add optional plan filter:

  ```text
  /?project=<project_id>&plan=<plan_doc_id>
  ```

- Add plan detail:

  ```text
  /plan/<plan_doc_id>
  ```

**UI:**

- Project switcher remains top-level.
- Before starting, inspect the current branch for existing project filter and card-chip work (`internal/web/server.go`, `internal/web/templates/kanban.html`, `internal/web/static/style.css`) and avoid clobbering it.
- Add plan switcher grouped by worktree:
  - All plans
  - `task_plan.md`
  - `docs/plans/...`
  - `progress.md`
- Cards show plan file chip.
- Plan detail shows:
  - plan metadata;
  - linked plans;
  - tasks from this plan;
  - latest source snapshot info.

**Tests:**

- Project page includes plan switcher when plan docs exist.
- Filtering by plan hides tasks from other source files.
- Plan detail renders linked plans.

### Phase 8: Raw Facts Mode

**Files:**

- Modify: `internal/web/server.go`
- Add: `internal/web/templates/facts.html`
- Modify: `internal/web/static/style.css`
- Test: `internal/web/server_test.go`

**Routes:**

```text
/facts
/worktree/<worktree_id>/facts
/snapshot/<snapshot_id>
```

**Snapshot page should show:**

- project/worktree;
- source file;
- timestamp;
- raw hash;
- commit SHA;
- parsed phases JSON;
- events emitted by that snapshot;
- task revisions for that snapshot once Phase 6 exists.

**UX principle:**

This mode should be faithful and inspectable, not pretty inference. Use dense tables, source paths, hashes, and JSON blocks.

### Phase 9: Time Machine UI

**Files:**

- Modify: `internal/web/server.go`
- Add: `internal/web/templates/timeline.html`
- Add: `internal/web/templates/snapshot_board.html`
- Modify: `internal/web/static/style.css`
- Test: `internal/web/server_test.go`

**Routes:**

```text
/worktree/<worktree_id>/timeline
/worktree/<worktree_id>/snapshot/<snapshot_id>
```

**Timeline page:**

- Snapshot list sorted descending.
- Each row includes:
  - timestamp;
  - source file;
  - commit SHA short form;
  - task count;
  - event count.

**Snapshot board page:**

- Render task revisions from that snapshot using kanban columns.
- Include source file and commit metadata.
- Provide links to raw snapshot facts and source context.

**Tests:**

- Timeline lists snapshots for one worktree only.
- Snapshot page shows historical task title/status.
- Changing current task state does not change old snapshot page.

## Acceptance Criteria

The implementation is acceptable when:

1. `thin-observer watch --once` ingests `docs/plans/*.md`.
2. Long-running `thin-observer watch` notices writes to `docs/plans/*.md`.
3. The project board can filter by plan file.
4. `task_plan.md` links to detailed plans are visible in UI.
5. Task detail can jump to source context around the original markdown line.
6. Snapshot pages show raw hash, parsed JSON, events, source file, and commit SHA.
7. Time machine can show historical task state for a worktree at a selected snapshot.
8. End-to-end smoke checks cover acceptance items 1-7 with real CLI/web flows:
   - `thin-observer watch --once` against a temp repo containing `docs/plans/*.md`;
   - local board route for project/plan filtering;
   - task source context route;
   - snapshot facts route;
   - worktree timeline/snapshot route.
9. `go test ./...`, `go vet ./...`, and `git diff --check` pass.

## Development Rules For Claude

- Use TDD for each behavior change.
- Do not write or edit watched markdown files.
- Do not use broad recursive `docs/` scanning; watch explicit plan directories.
- Keep inference conservative and explainable.
- If a rule is ambiguous, store raw facts and expose uncertainty rather than pretending the system knows.
- Prefer small commits by phase.

## Suggested Execution Order

1. Phase 1: `docs/plans/*.md` discovery/watch.
2. Phase 2: plain task-section parser semantics.
3. Phase 3: markdown link extraction.
4. Phase 4: `plan_doc` / `plan_link` schema and ingest.
5. Phase 5: commit SHA capture.
6. Phase 6: `task_revision`.
7. Phase 7: plan view UI.
8. Phase 8: raw facts mode.
9. Phase 9: time machine UI.

Do not start with Time Machine UI before task revisions exist. It will otherwise reconstruct history from mutable current rows and give false results.
