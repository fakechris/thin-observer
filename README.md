# thin-observer

**A passive observer for coding-agent plan files.** Watches `plan.md` / `todo.md` / `progress.md` across all your worktrees and projects a kanban board out of them — without asking the agent to cooperate.

Three pains it fixes:

1. You can't see what the agent is doing.
2. The agent forgets the back half of its own plan after 3 turns.
3. After enough edits, the earliest tasks drift away and disappear.

## Why observer-side?

Most "agent-coordination" tools require the agent to call a CLI (`planctl split-task …`), declare stable task IDs (`[T-07]`), or install a skill. That's fragile — every agent vendor has a different integration, and humans will always edit markdown faster than they call a tool.

thin-observer does the opposite. It watches your plan files, snapshots them, and infers task identity + lineage (renames, splits, merges) with heuristics. **Zero agent cooperation required.** If the agent is capable enough to write markdown, it's already integrated.

## Install

```bash
go install github.com/chris/thin-observer/cmd/thin-observer@latest
```

Or from source:

```bash
git clone https://github.com/chris/thin-observer
cd thin-observer
go build -o thin-observer ./cmd/thin-observer
```

## Quick start

```bash
# 1. Point it at your worktrees.
mkdir -p ~/.config/thin-observer
cat > ~/.config/thin-observer/config.yaml <<'EOF'
projects:
  - name: my-app
    path: /path/to/repo
EOF

# 2. Ingest what's there right now.
thin-observer watch --once

# 3. See what all your agents are doing.
thin-observer status

# 4. Open the board.
thin-observer board    # http://127.0.0.1:7777

# 5. Paste full recap into your next agent session.
thin-observer recap my-app
```

## Commands

| | |
|---|---|
| `thin-observer watch` | daemon: fsnotify plan files, ingest on change |
| `thin-observer watch --once` | one-shot discovery + ingest, then exit |
| `thin-observer status` | one-screen overview table of all worktrees |
| `thin-observer recap <worktree>` | plain-text recap, safe to pipe into an agent |
| `thin-observer task <id>` | full history + events + lineage for one task |
| `thin-observer board [--addr]` | read-only web kanban + archive + lineage |
| `thin-observer parse <file.md>` | dump parser output as JSON (debug) |
| `thin-observer init` | create state dir + SQLite db |

## What it looks like

```
WORKTREE                  PROJECT               ACTIVE    PEND    DONE    LOST         LAST  IN PROGRESS
api-compat-3              my-app                     1       4       7       0           4m  Add auth header handler
ui-rebuild                my-app                     2       2       3       1          12m  Migrate Kanban column; Wire up filter
chore-cleanup             infra                      0       0       5       0           3h  -
```

## How it works

1. **parser** — tolerant markdown parser for `plan.md`, `todo.md`, `progress.md`, etc. Accepts frontmatter + phase headings + checklists, tolerates messy mixes.
2. **discovery** — walks `roots:` + `projects:` from config, enumerates git worktrees.
3. **watcher** — fsnotify with 500ms debounce.
4. **lineage** — heuristic inferrer (exact → alias → Levenshtein rename → token-overlap split/merge). Every decision carries a confidence score.
5. **ingest** — applies decisions; unmatched tasks get `missing_in_rev++` and become `lost` after two consecutive misses.
6. **board** — go:embed HTML templates, no build step, no JS framework.

All state lives in `~/.local/state/thin-observer/`:

```
db.sqlite           # task + snapshot + event + override rows
events.jsonl        # append-only audit log (planned)
snapshots/          # raw plan markdown, by hash (planned)
```

## The seven invariants

1. Markdown is the agent's truth layer. thin-observer never writes it back.
2. Zero agent cooperation. No `[T-xx]`, no CLI calls, no skills.
3. Every lineage decision has a confidence score; low-confidence results go to "Needs Attention", not silent merges.
4. Fail loud. Drift and lost tasks warn; they don't auto-correct.
5. Events are append-only. Archived log rotations never delete history.
6. Worktree death ≠ task death. Closed worktrees go to Archive; their tasks stay queryable.
7. Board is read-only. Manual overrides live only in observer state.

## Status

MVP. Observer-side task inference + web kanban work end-to-end for the author's own coding workflow. Rough edges expected; PRs welcome.

See [`docs/archived-prd-2026-04-20/REASONS-ARCHIVED.md`](docs/archived-prd-2026-04-20/REASONS-ARCHIVED.md) for why this project took the observer-side path instead of the cross-agent protocol direction of the original PRD.

## License

Apache 2.0 — see [LICENSE](LICENSE).
